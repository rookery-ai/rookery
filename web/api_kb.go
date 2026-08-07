package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ilijad1/rookery/internal/convert"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/export"
	"github.com/ilijad1/rookery/internal/iolimit"
	"github.com/ilijad1/rookery/internal/vault"
	"github.com/labstack/echo/v4"
)

// maxUploadBytes caps a KB file upload. Conversion (internal/convert) allocates
// proportional to input size, and an unbounded upload is a trivial
// memory-exhaustion vector on a home server.
const maxUploadBytes = 25 << 20 // 25 MiB

// maxUploadOverhead is slack added on top of maxUploadBytes when capping the
// raw request body: multipart encoding adds boundary/header bytes beyond the
// file's own content, so a body-level cap set to exactly maxUploadBytes would
// reject a file that is itself right at the limit. The overhead cap is a
// coarse memory-exhaustion backstop; the real, exact limit is enforced
// against fh.Size and the read file content below.
const maxUploadOverhead = 1 << 20 // 1 MiB

// kbInlineMax is the size cap (bytes) under which a non-markdown file's
// content is sniffed and inlined as kind:"code". At or above it, the file is
// always kind:"binary" regardless of its content — a large valid-UTF-8 text
// file is still not something we want to dump into a JSON response body /
// render into the DOM as a live-edited <pre>. See spec §7.
const kbInlineMax = 1 << 20 // 1 MiB

// registerKBAPI registers the JSON knowledge-base endpoints on the given group
// (already guarded by requireOwnerAPI + requireActiveWorkspaceAPI +
// requireSetupCompleteAPI). Every handler here is a JSON port of the
// corresponding template handler in web/handlers_kb.go and reuses the exact
// same vault/helper calls — all path safety comes from vault.Resolve inside
// those calls, never re-implemented here.
//
// GET /api/v1/kb/raw re-registers the EXISTING s.rawKBNote handler UNCHANGED
// (it already serves a raw file download; no JSON envelope to apply).
func (s *Server) registerKBAPI(g *echo.Group) {
	g.GET("/kb/tree", s.apiKBTree)
	g.GET("/kb/note", s.apiGetKBNote)
	g.PUT("/kb/note", s.apiSaveKBNote)
	g.POST("/kb/new", s.apiNewKBNote)
	g.DELETE("/kb/note", s.apiDeleteKBNoteAPI)
	g.POST("/kb/rename", s.apiRenameKBNote)
	g.GET("/kb/search", s.apiSearchKB)
	g.GET("/kb/resolve", s.apiResolveKBLink)
	g.GET("/kb/raw", s.rawKBNote)
	g.PUT("/kb/order", s.apiSaveKBOrder)
	g.POST("/kb/upload", s.apiUploadKBFile)
	g.PUT("/kb/icon", s.apiSaveKBIcon)
	g.GET("/kb/folders", s.apiKBFolders)
	g.GET("/kb/export", s.apiExportKBNote)
	g.GET("/kb/export/formats", s.apiExportFormats)
	g.POST("/kb/asset", s.apiUploadKBAsset)
	g.GET("/kb/assets", s.apiListKBAssets)
	g.POST("/kb/assist", s.apiKBAssist)
}

// ── Assets (images & attachments) ─────────────────────────────────────────────
//
// Unlike /kb/upload (which converts a document INTO markdown), an asset is
// stored as raw bytes under the vault's assets/ folder and referenced from a
// note as ![alt](assets/…) or [file](assets/…). This is how images/attachments
// embedded via the editor's / menu are persisted. Serving happens through
// /kb/raw, which sniffs the content type so an <img> actually renders.

// imageExts is the set of extensions the KB treats as inline-renderable images
// (for the FileViewer image branch, the assets picker, and kind:"image").
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".svg": true, ".bmp": true, ".ico": true, ".avif": true,
}

func isImagePath(rel string) bool {
	return imageExts[strings.ToLower(path.Ext(rel))]
}

// escapePathParam URL-escapes a vault-relative path for use as the ?path= query
// value in a served URL.
func escapePathParam(rel string) string {
	return url.QueryEscape(rel)
}

// assetName builds a collision-resistant, still-recognizable filename for a
// stored asset: the sanitized original stem + a short random suffix + the
// original extension. Keeping the stem means an asset is identifiable in the KB
// browser rather than an opaque UUID.
func assetName(filename string) string {
	ext := strings.ToLower(path.Ext(filename))
	stem := strings.TrimSuffix(path.Base(filename), path.Ext(filename))
	stem = slugifyAsset(stem)
	if stem == "" {
		stem = "file"
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%s%s", stem, hex.EncodeToString(b[:]), ext)
}

// slugifyAsset lowercases and replaces any run of non-alphanumeric characters
// with a single dash, trimming leading/trailing dashes — so a stored asset path
// is filesystem- and URL-safe.
func slugifyAsset(s string) string {
	var out strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			out.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(out.String(), "-")
}

type apiKBAsset struct {
	Path string `json:"path"`
	URL  string `json:"url"`
}

// apiUploadKBAsset stores an uploaded image/file as raw bytes under assets/ and
// returns its vault-relative path + a served URL for embedding. Shares the 25
// MiB iolimit cap with every other ingest door.
// POST /api/v1/kb/asset multipart {file} → 200 {path, url, kind, content_type}
func (s *Server) apiUploadKBAsset(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxUploadBytes+maxUploadOverhead)
	fh, err := c.FormFile("file")
	if err != nil {
		if isRequestTooLarge(err) {
			return jsonErr(c, http.StatusRequestEntityTooLarge, "too_large",
				fmt.Sprintf("upload exceeds the %d byte limit", maxUploadBytes))
		}
		return jsonErr(c, http.StatusBadRequest, "invalid_request", "no file uploaded")
	}
	if fh.Size > maxUploadBytes {
		return jsonErr(c, http.StatusRequestEntityTooLarge, "too_large",
			fmt.Sprintf("file is %d bytes; the limit is %d", fh.Size, maxUploadBytes))
	}
	src, err := fh.Open()
	if err != nil {
		return jsonErr(c, http.StatusBadRequest, "invalid_request", "could not read the upload")
	}
	defer src.Close()
	data, err := iolimit.ReadCapped(src, maxUploadBytes)
	if err != nil {
		if errors.Is(err, iolimit.ErrTooLarge) {
			return jsonErr(c, http.StatusRequestEntityTooLarge, "too_large",
				fmt.Sprintf("file exceeds the %d byte limit", maxUploadBytes))
		}
		return jsonErr(c, http.StatusBadRequest, "invalid_request", "could not read the upload")
	}

	// Editor images and attachments are uploads too, so they land in the same
	// uploads/ folder as every other ingest door rather than a second folder of
	// their own. assetName already appends four random bytes, so this cannot
	// collide with the originals ImportFile writes here via uniquePath.
	rel := path.Join(vault.FilesDir, assetName(fh.Filename))
	if err := s.vault.WriteNote(u.ID, rel, data); err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "could not store asset: "+err.Error())
	}
	kind := "file"
	if isImagePath(rel) {
		kind = "image"
	}
	return c.JSON(http.StatusOK, map[string]any{
		"path":         rel,
		"url":          "/api/v1/kb/raw?path=" + escapePathParam(rel),
		"kind":         kind,
		"content_type": detectContentType(rel, data),
	})
}

// apiListKBAssets lists every image file in the vault (for the editor's
// "insert from knowledge base" image picker).
// GET /api/v1/kb/assets → 200 {"assets":[{path,url}]}
func (s *Server) apiListKBAssets(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	paths, err := s.vault.ListImageFiles(u.ID)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "could not list assets: "+err.Error())
	}
	out := make([]apiKBAsset, 0, len(paths))
	for _, p := range paths {
		out = append(out, apiKBAsset{Path: p, URL: "/api/v1/kb/raw?path=" + escapePathParam(p)})
	}
	return c.JSON(http.StatusOK, map[string]any{"assets": out})
}

// detectContentType picks a MIME type from the file extension first (exact for
// our known asset kinds) and falls back to sniffing the bytes.
func detectContentType(rel string, data []byte) string {
	if ct := mime.TypeByExtension(path.Ext(rel)); ct != "" {
		return ct
	}
	return http.DetectContentType(data)
}

// ── Icons ────────────────────────────────────────────────────────────────────
//
// Like tree ordering (kbOrderSettingKey above), custom per-node emoji icons are
// stored OUT of band — one JSON blob per workspace in workspace_settings — so
// nothing is written into the vault itself (a stray icon sidecar would show up
// in the tree and in agents' reads of the KB). Shape: {"<rel path>": "<emoji>"}.
//
// Unlike kb_order (which keys by name-WITHIN-a-dir, so a rename just drops the
// entry out of its list and degrades gracefully), icons key by FULL PATH — so a
// rename/move/delete that didn't maintain this map would orphan the icon or
// leave it pointing at a path that no longer exists. apiRenameKBNote and
// apiDeleteKBNoteAPI below re-key/drop entries accordingly (incl. every
// descendant when a folder moves or is deleted).
const kbIconsSettingKey = "kb_icons"

type apiKBIconRequest struct {
	Path string `json:"path"`
	Icon string `json:"icon"`
}

// loadKBIcons reads the whole path→emoji map. A missing/corrupt value is not an
// error: icons are a preference, and losing them degrades to the default lucide
// icons rather than breaking the tree.
func (s *Server) loadKBIcons(workspaceID string) map[string]string {
	out := map[string]string{}
	raw, err := s.db.GetSetting(workspaceID, kbIconsSettingKey)
	if err != nil || raw == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]string{}
	}
	return out
}

func (s *Server) saveKBIcons(workspaceID string, icons map[string]string) error {
	blob, err := json.Marshal(icons)
	if err != nil {
		return err
	}
	return s.db.SetSetting(workspaceID, kbIconsSettingKey, string(blob))
}

// migrateIconKeys re-keys the icon map when a path moves. It moves the exact
// entry for `from`→`to` AND, so a folder move carries its whole subtree's
// icons, every entry keyed under `from/…` → `to/…`. Returns whether anything
// changed (so the caller can skip a needless DB write). Pure map surgery — the
// caller decides when to persist.
func migrateIconKeys(icons map[string]string, from, to string) bool {
	changed := false
	fromPrefix := from + "/"
	toPrefix := to + "/"
	for k, v := range icons {
		var nk string
		switch {
		case k == from:
			nk = to
		case strings.HasPrefix(k, fromPrefix):
			nk = toPrefix + strings.TrimPrefix(k, fromPrefix)
		default:
			continue
		}
		delete(icons, k)
		icons[nk] = v
		changed = true
	}
	return changed
}

// dropIconKeys removes the entry for `p` and every descendant (`p/…`). Returns
// whether anything changed.
func dropIconKeys(icons map[string]string, p string) bool {
	changed := false
	prefix := p + "/"
	for k := range icons {
		if k == p || strings.HasPrefix(k, prefix) {
			delete(icons, k)
			changed = true
		}
	}
	return changed
}

// apiSaveKBIcon sets (or, with an empty icon, clears) a node's custom emoji.
// PUT /api/v1/kb/icon {path,icon} → 200 {"ok":true}
func (s *Server) apiSaveKBIcon(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	var req apiKBIconRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	// Path-safety discipline even though only settings are touched (matches
	// apiSaveKBOrder): a traversal attempt must not be usable to plant a key.
	rel := cleanKBParam(req.Path)
	if rel == "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_path", "a path is required")
	}
	if _, err := s.vault.Resolve(u.ID, rel); err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "invalid path: "+err.Error())
	}
	icons := s.loadKBIcons(u.ID)
	icon := strings.TrimSpace(req.Icon)
	if icon == "" {
		delete(icons, rel)
	} else {
		icons[rel] = icon
	}
	if err := s.saveKBIcons(u.ID, icons); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "could not save icon")
	}
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiKBFolders returns every folder path in the vault (root "" included), for
// the new-note "Location" picker and the bulk-Move picker. Walks the vault dirs
// only, skipping the hidden .kb internal dir. Ordered depth-first, parents
// before children.
// GET /api/v1/kb/folders → 200 {"folders":["","notes","projects",...]}
func (s *Server) apiKBFolders(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	_ = s.vault.EnsureScaffold(u.ID)
	folders, err := s.vault.ListFolders(u.ID)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "could not list folders: "+err.Error())
	}
	// Hidden in the tree, so it must be hidden here too — otherwise the legacy
	// assets/ folder is only half-hidden and still selectable as a destination.
	visible := make([]string, 0, len(folders))
	for _, f := range folders {
		if f == legacyAssetsDir {
			continue
		}
		visible = append(visible, f)
	}
	return c.JSON(http.StatusOK, map[string]any{"folders": orEmpty(visible)})
}

// ── Tree ordering ────────────────────────────────────────────────────────────
//
// The vault is a plain directory tree, so it carries no user-chosen ordering of
// its own — the browser derives one (user content before system dirs, dirs
// before files, then alphabetically). Drag-to-reorder needs that choice to
// survive a reload, so it is stored OUT of band, as one JSON blob per
// workspace in the existing workspace_settings table: no migration, no new
// table, and nothing written into the vault itself (a stray ordering file
// would show up in the tree and in agents' reads of the KB).
//
// Shape: {"<dir rel path>": ["name1","name2", …]}, where "" is the vault root.
// Names, not paths — a rename moves the entry out of the list, which is
// exactly right: an unlisted name falls back to the derived ordering rather
// than pinning a stale position.
const kbOrderSettingKey = "kb_order"

type apiKBOrderRequest struct {
	Dir   string   `json:"dir"`
	Names []string `json:"names"`
}

// loadKBOrder reads the whole ordering map. A missing or corrupt value is not
// an error: ordering is a preference, and losing it degrades to the derived
// sort rather than breaking the tree.
func (s *Server) loadKBOrder(workspaceID string) map[string][]string {
	out := map[string][]string{}
	raw, err := s.db.GetSetting(workspaceID, kbOrderSettingKey)
	if err != nil || raw == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string][]string{}
	}
	return out
}

// apiSaveKBOrder records the sibling order for a single directory.
// PUT /api/v1/kb/order {dir,names} → 200 {"ok":true}
func (s *Server) apiSaveKBOrder(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	var req apiKBOrderRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	// cleanKBParam + Resolve so a traversal attempt can't be used to plant an
	// arbitrary key, even though nothing here touches the filesystem — the
	// same path discipline every other handler in this file follows.
	dir := cleanKBParam(req.Dir)
	if _, err := s.vault.Resolve(u.ID, dir); err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "invalid folder: "+err.Error())
	}

	order := s.loadKBOrder(u.ID)
	if len(req.Names) == 0 {
		delete(order, dir)
	} else {
		order[dir] = req.Names
	}
	blob, err := json.Marshal(order)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "could not encode order")
	}
	if err := s.db.SetSetting(u.ID, kbOrderSettingKey, string(blob)); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "could not save order")
	}
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// kbSystemDirs are the top-level vault directories that are system-managed
// (reflected from the DB or otherwise not user-authored knowledge), minus
// .kb, which vault.List never surfaces at all. The template browser has no
// explicit "system" flag of its own — it just relies on enrichKBDisplayNames
// to give these dirs friendlier labels — so this is this endpoint's own
// derivation: a root-level node whose name is one of these is marked
// system:true.
//
// `inbox` and `reminders` are absent for the same reason they left
// protectedTopDirs and kbSystemFolderLabels: notifications and reminders are no
// longer reflected into the vault, so the platform does not own those names and
// a user folder called "inbox" is the user's own knowledge, not chrome.
// legacyAssetsDir is the folder editor images used to land in before uploads/
// consolidated every ingest door. Nothing writes to it any more.
//
// It is HIDDEN rather than migrated: existing notes reference their images as
// `![](assets/foo.png)`, and those keep resolving because /kb/raw goes through
// vault.Resolve, never the tree listing. Rewriting image references across
// every user note is the only change in this area with real corruption risk,
// and it buys nothing once the folder is out of sight.
//
// Hiding also closes a latent hazard: `assets` was marked system:true but was
// absent from both protectedTopDirs and the SPA's PROTECTED_TOP_DIRS, so a user
// could rename or delete it and orphan every image link in the vault.
const legacyAssetsDir = "assets"

// isHiddenLegacyAssetsDir reports whether a tree/folder entry is the legacy
// root-level assets/ directory.
//
// Root-level ONLY: skills keep their own skills/<name>/assets/ directory
// (skillstore.go), and a blanket name match would hide those too.
func isHiddenLegacyAssetsDir(isRoot bool, name string, isDir bool) bool {
	return isRoot && isDir && name == legacyAssetsDir
}

var kbSystemDirs = map[string]bool{
	"agents": true, "chats": true, "memory": true,
	"skills": true, "assets": true,
}

// protectedPathMessage returns a user-facing explanation when rel is a
// DB-backed, system-managed path the user must not delete or rename from the KB
// browser (see vault.IsUserMutationProtected), or "" when the mutation is
// allowed. The message points the user at the item's own page — deleting the
// record there is the sanctioned path that also cleans up the vault files,
// whereas deleting the files here would orphan the row.
func protectedPathMessage(rel string) string {
	if !vault.IsUserMutationProtected(rel) {
		return ""
	}
	top, _, _ := strings.Cut(strings.TrimPrefix(rel, "/"), "/")
	switch top {
	case "agents":
		return "This is an agent's files — delete the agent from the Agents page instead."
	case "chats":
		return "This is a chat transcript — delete the chat from the Chats page instead."
	case "inbox":
		return "This is an inbox notification — delete it from the inbox instead."
	case "skills":
		return "This is a skill's files — delete the skill from the Skills page instead."
	case "reminders":
		return "This is a reminder — delete it from the reminders list instead."
	default:
		return "This folder is managed by the system and can't be changed here."
	}
}

// ── DTOs ─────────────────────────────────────────────────────────────────────

type apiKBNode struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Path        string `json:"path"`
	IsDir       bool   `json:"is_dir"`
	System      bool   `json:"system"`
	// Icon is the user's custom emoji for this node ("" = client uses its
	// default lucide icon). Stored out-of-band in kb_icons; see loadKBIcons.
	Icon string `json:"icon"`
}

type apiKBTreeResponse struct {
	Path  string      `json:"path"`
	Nodes []apiKBNode `json:"nodes"`
	// Order is the user's drag-chosen sibling order for this directory, by
	// node NAME, or empty when they've never reordered it. Served alongside
	// the nodes rather than from a second endpoint so opening a folder stays
	// one round trip. The client treats it as the primary sort key and falls
	// back to its own derived ordering for anything not listed (e.g. a file
	// an agent wrote since the last drag).
	Order []string `json:"order"`
}

type apiKBNoteResponse struct {
	Path      string   `json:"path"`
	Content   string   `json:"content"`
	HTML      string   `json:"html"`
	Backlinks []string `json:"backlinks"`
	// Kind discriminates how the SPA renders this file: "markdown" (the
	// WYSIWYG/raw editor, unchanged), "code" (read-only monospace view —
	// any non-markdown file whose bytes are valid UTF-8 and under
	// kbInlineMax), or "binary" (a Download-only panel; Content is omitted).
	// Decided by content sniffing, not an extension allowlist — see spec §7.
	Kind string `json:"kind"`
	// Icon is the note's custom emoji ("" = default). Same kb_icons source as
	// the tree nodes.
	Icon string `json:"icon"`
}

type apiSaveKBNoteRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type apiNewKBNoteRequest struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

type apiRenameKBNoteRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type apiKBSearchHit struct {
	Path string `json:"path"`
	// Title is the resolved display name for Path — the same one the KB tree
	// shows. Sent alongside Path (rather than instead of it) because a
	// reflected note's filename is a UUID, so the path alone identifies nothing
	// to a human, while the title alone says nothing about where the file lives.
	Title   string `json:"title"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

// ── Handlers ─────────────────────────────────────────────────────────────────

// vaultErrStatus maps a vault error to an API status+code. vault.Resolve
// (called internally by every vault method) returns vault.ErrEscapes for any
// path-traversal/absolute-path attempt, which we surface as 400 invalid_path;
// a missing file/dir (os.ErrNotExist, e.g. reading a note that was never
// created) is 404 not_found; anything else is a generic 500.
func vaultErrStatus(err error) (int, string) {
	if errors.Is(err, vault.ErrEscapes) {
		return http.StatusBadRequest, "invalid_path"
	}
	if errors.Is(err, os.ErrNotExist) {
		return http.StatusNotFound, "not_found"
	}
	return http.StatusInternalServerError, "internal"
}

func (s *Server) kbUnavailable(c echo.Context) error {
	return jsonErr(c, http.StatusNotImplemented, "kb_unavailable", "knowledge base not available")
}

// apiKBTree ports showKB + enrichKBDisplayNames.
// GET /api/v1/kb/tree?path= → 200 {"path","nodes":[{name,display_name,path,is_dir,system}]}
func (s *Server) apiKBTree(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	_ = s.vault.EnsureScaffold(u.ID)

	rel := cleanKBParam(c.QueryParam("path"))
	nodes, err := s.vault.List(u.ID, rel)
	if err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "could not open folder: "+err.Error())
	}
	s.enrichKBDisplayNames(u.ID, rel, nodes)

	icons := s.loadKBIcons(u.ID)
	isRoot := rel == ""
	out := make([]apiKBNode, 0, len(nodes))
	for _, n := range nodes {
		if isHiddenLegacyAssetsDir(isRoot, n.Name, n.IsDir) {
			continue
		}
		display := n.DisplayName
		if display == "" {
			display = n.Name
		}
		out = append(out, apiKBNode{
			Name:        n.Name,
			DisplayName: display,
			Path:        n.Path,
			IsDir:       n.IsDir,
			System:      isRoot && n.IsDir && kbSystemDirs[n.Name],
			Icon:        icons[n.Path],
		})
	}
	return c.JSON(http.StatusOK, apiKBTreeResponse{
		Path:  rel,
		Nodes: out,
		Order: orEmpty(s.loadKBOrder(u.ID)[rel]),
	})
}

// apiGetKBNote ports viewKBNote (+editKBNote's raw-content read for non-markdown
// files). Markdown notes get rendered HTML + backlinks (via s.vault.Backlinks,
// same call viewKBNote makes) and are always kind:"markdown", unconditionally
// (unchanged behavior — no size cap applies to notes, matching the editor's
// pre-existing full-read path). Any other file is content-sniffed (NOT judged
// by extension — agents invent file types, see spec §7): kind:"code" when its
// bytes are valid UTF-8 and it's under kbInlineMax, else kind:"binary" with
// Content left empty (the SPA falls back to the raw-download link for it).
// The size check comes from a Stat, before any full read, so an oversize file
// is never pulled into memory just to be discarded.
// GET /api/v1/kb/note?path= → 200 {"path","content","html","backlinks":[...],"kind"}
func (s *Server) apiGetKBNote(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	rel := cleanKBParam(c.QueryParam("path"))

	if strings.EqualFold(path.Ext(rel), ".md") {
		data, err := s.vault.ReadNote(u.ID, rel)
		if err != nil {
			status, code := vaultErrStatus(err)
			return jsonErr(c, status, code, "could not open note: "+err.Error())
		}
		resp := apiKBNoteResponse{
			Path:      rel,
			Content:   string(data),
			Backlinks: []string{},
			Kind:      "markdown",
			Icon:      s.loadKBIcons(u.ID)[rel],
		}
		resp.HTML = string(s.renderMarkdown(u.ID, resp.Content))
		if back, err := s.vault.Backlinks(u.ID, rel); err == nil {
			resp.Backlinks = orEmpty(back)
		}
		return c.JSON(http.StatusOK, resp)
	}

	// Non-markdown: resolve + Stat first so an oversize file is classified
	// binary without ever reading its bytes into memory.
	abs, err := s.vault.Resolve(u.ID, rel)
	if err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "could not open note: "+err.Error())
	}
	fi, err := os.Stat(abs)
	if err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "could not open note: "+err.Error())
	}

	resp := apiKBNoteResponse{Path: rel, Backlinks: []string{}, Icon: s.loadKBIcons(u.ID)[rel]}
	// An image file renders inline in the FileViewer (via /kb/raw) regardless of
	// size — its bytes are never inlined into this JSON response.
	if isImagePath(rel) {
		resp.Kind = "image"
		return c.JSON(http.StatusOK, resp)
	}
	if fi.Size() > kbInlineMax {
		resp.Kind = "binary"
		return c.JSON(http.StatusOK, resp)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "could not open note: "+err.Error())
	}
	if utf8.Valid(data) {
		resp.Kind = "code"
		resp.Content = string(data)
	} else {
		resp.Kind = "binary"
	}
	return c.JSON(http.StatusOK, resp)
}

// agentIDFromStatePath reports whether rel (already cleaned by cleanKBParam)
// points at an agent's state.md — i.e. matches agents/<id>/state.md with a
// non-empty <id> and no deeper nesting — and if so returns that id. It uses
// path.Clean first so a trailing slash or a "./" prefix doesn't defeat the
// match, and compares the fixed segments case-insensitively (a real vault
// path is lowercase, but this guard is a safety net, not a path-safety
// primitive — vault.Resolve still owns actual containment).
func agentIDFromStatePath(rel string) (string, bool) {
	cleaned := path.Clean("/" + rel) // leading slash makes Clean fold "." to "/"
	cleaned = strings.TrimPrefix(cleaned, "/")
	parts := strings.Split(cleaned, "/")
	if len(parts) != 3 {
		return "", false
	}
	if !strings.EqualFold(parts[0], "agents") || !strings.EqualFold(parts[2], "state.md") {
		return "", false
	}
	id := parts[1]
	if id == "" || id == "." || id == ".." {
		return "", false
	}
	return id, true
}

// apiSaveKBNote ports handleSaveKBNote.
// PUT /api/v1/kb/note {path,content} → 200 {"ok":true}
func (s *Server) apiSaveKBNote(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	var req apiSaveKBNoteRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	rel := cleanKBParam(req.Path)
	if rel == "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_path", "a note path is required")
	}
	if !strings.Contains(path.Base(rel), ".") {
		rel += ".md" // default new notes to markdown, matching handleSaveKBNote
	}
	// The runner writes agents/<id>/state.md at the end of every run (see
	// internal/agentdesigner/statefile.go); racing that write with a manual
	// KB edit would corrupt whichever one lands second. Refuse the save
	// while that agent has a run in flight — checked via the SAME tracker
	// the agent detail page's "Running…" badge and the run-already-running
	// 202 use (web/run_tracker.go's isAgentRunning), not a new mechanism.
	// A draft build dir (agents/draft_<slug>/state.md) never matches a live
	// agent id in s.runs or agent_runs, so isAgentRunning naturally returns
	// false for it — no special-casing needed.
	//
	// This MUST run on the finalized rel, AFTER the .md default above: a PUT
	// to agents/<id>/state (no extension) lands on state.md, so guarding the
	// raw input let any direct API caller write a running agent's state with
	// a 200. The spec's whole premise is that the frontend cannot be trusted,
	// so the check belongs on the path actually about to be written.
	if agentID, ok := agentIDFromStatePath(rel); ok && s.isAgentRunning(agentID) {
		return jsonErr(c, http.StatusConflict, "agent_running",
			"this agent is running right now — its state.md will be overwritten when the run finishes. Wait for it to finish, then save your edit.")
	}
	content := normalizeNewlines(req.Content)
	if err := s.vault.WriteNote(u.ID, rel, []byte(content)); err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "save failed: "+err.Error())
	}
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiNewKBNote ports handleNewKBNote. The template form takes dir+name+kind
// fields; the JSON shape here is a single {path,is_dir} — path is split into
// dir=path.Dir(path)/name=path.Base(path) to drive the exact same vault calls.
// POST /api/v1/kb/new {path,is_dir} → 201 {"ok":true}
func (s *Server) apiNewKBNote(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	var req apiNewKBNoteRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	rel := cleanKBParam(req.Path)
	if rel == "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_path", "a path is required")
	}

	if req.IsDir {
		// Represent a folder by seeding a hidden keep note so it persists and is
		// browsable, matching handleNewKBNote's kind=="folder" branch.
		if err := s.vault.WriteNote(u.ID, path.Join(rel, ".keep"), []byte("")); err != nil {
			status, code := vaultErrStatus(err)
			return jsonErr(c, status, code, "create folder failed: "+err.Error())
		}
		return c.JSON(http.StatusCreated, apiOKResponse{OK: true})
	}

	name := path.Base(rel)
	if !strings.Contains(name, ".") {
		rel += ".md"
	}
	if err := s.vault.WriteNote(u.ID, rel, []byte("# "+strings.TrimSuffix(name, ".md")+"\n\n")); err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "create note failed: "+err.Error())
	}
	return c.JSON(http.StatusCreated, apiOKResponse{OK: true})
}

// apiDeleteKBNoteAPI ports handleDeleteKBNote.
// DELETE /api/v1/kb/note?path= → 200 {"ok":true}
func (s *Server) apiDeleteKBNoteAPI(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	rel := cleanKBParam(c.QueryParam("path"))
	if msg := protectedPathMessage(rel); msg != "" {
		return jsonErr(c, http.StatusForbidden, "protected_path", msg)
	}
	if err := s.vault.Delete(u.ID, rel); err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "delete failed: "+err.Error())
	}
	// Drop any custom icon(s) for the removed path and its descendants so the
	// map doesn't accumulate orphans. Non-fatal on error (the file is gone).
	if icons := s.loadKBIcons(u.ID); dropIconKeys(icons, rel) {
		if err := s.saveKBIcons(u.ID, icons); err != nil {
			slog.Warn("kb delete: icon cleanup failed", "workspace_id", u.ID, "path", rel, "err", err)
		}
	}
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiRenameKBNote ports handleRenameKBNote.
// POST /api/v1/kb/rename {from,to} → 200 {"ok":true}
func (s *Server) apiRenameKBNote(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	var req apiRenameKBNoteRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	from := cleanKBParam(req.From)
	to := cleanKBParam(req.To)
	if from == "" || to == "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_path", "both source and destination are required")
	}
	// A rename that touches a system-managed area on EITHER end — moving a
	// protected item out, or dropping a user note into one — would orphan or
	// shadow a DB-backed record, so refuse both.
	if msg := protectedPathMessage(from); msg != "" {
		return jsonErr(c, http.StatusForbidden, "protected_path", msg)
	}
	if msg := protectedPathMessage(to); msg != "" {
		return jsonErr(c, http.StatusForbidden, "protected_path",
			"can't move items into a system-managed folder (agents, chats, inbox, skills, reminders).")
	}
	// vault.Rename bottoms out in os.Rename, which SILENTLY replaces an
	// existing destination. That was survivable while renaming was a typed,
	// deliberate act; it is not once a mis-aimed drag in the tree can trigger
	// it, so refuse the collision instead of destroying the file already
	// there. Resolve (not raw path joining) keeps the containment guarantee.
	if to != from {
		if abs, err := s.vault.Resolve(u.ID, to); err == nil {
			if _, statErr := os.Stat(abs); statErr == nil {
				return jsonErr(c, http.StatusConflict, "already_exists",
					"“"+to+"” already exists — rename or move it somewhere else.")
			}
		}
	}
	if err := s.vault.Rename(u.ID, from, to); err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "rename failed: "+err.Error())
	}
	// Carry any custom icon(s) to the new path — the exact entry and, for a
	// folder move, every descendant's. A failure here is non-fatal: the file
	// moved fine, only its icon is now stale, so log-and-continue rather than
	// reporting the rename as failed.
	if icons := s.loadKBIcons(u.ID); migrateIconKeys(icons, from, to) {
		if err := s.saveKBIcons(u.ID, icons); err != nil {
			slog.Warn("kb rename: icon re-key failed", "workspace_id", u.ID, "from", from, "to", to, "err", err)
		}
	}
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiResolveKBLink resolves a [[wikilink]] target to the note it points at,
// reusing the exact same LinkIndex the vault builds for backlinks/rendering
// (internal/vault/links.go) — no resolution logic is duplicated here.
// GET /api/v1/kb/resolve?link=<target> → 200 {"path":"notes/target.md"} or 404 not_found.
func (s *Server) apiResolveKBLink(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	link := strings.TrimSpace(c.QueryParam("link"))
	if link == "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_link", "link is required")
	}
	idx, err := s.vault.BuildLinkIndex(u.ID)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "could not build link index: "+err.Error())
	}
	rel := idx.Resolve(link)
	if rel == "" {
		return jsonErr(c, http.StatusNotFound, "not_found", "no note matches ["+link+"]")
	}
	return c.JSON(http.StatusOK, map[string]string{"path": rel})
}

// apiSearchKB ports searchKB.
// GET /api/v1/kb/search?q= → 200 {"hits":[{path,title,line,snippet}]}
func (s *Server) apiSearchKB(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	q := strings.TrimSpace(c.QueryParam("q"))
	if q == "" {
		return jsonErr(c, http.StatusBadRequest, "empty_query", "q is required")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
	defer cancel()
	hits, err := s.vault.NewSearcher().Search(ctx, u.ID, q)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "search failed: "+err.Error())
	}
	out := make([]apiKBSearchHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, apiKBSearchHit{
			Path: h.Path, Title: s.kbDisplayTitle(u.ID, h.Path), Line: h.Line, Snippet: h.Snippet,
		})
	}
	return c.JSON(http.StatusOK, map[string]any{"hits": out})
}

// stripFrontmatter removes a leading YAML frontmatter block (--- … ---) from a
// note body so an export document doesn't render the raw metadata. Mirrors the
// frontend's splitFrontmatter for the export path; deliberately minimal (a note
// either opens with a fenced --- block or it doesn't).
func stripFrontmatter(md string) string {
	if !strings.HasPrefix(md, "---\n") && !strings.HasPrefix(md, "---\r\n") {
		return md
	}
	rest := md[strings.IndexByte(md, '\n')+1:]
	// Find the closing fence line (--- on its own line).
	for i := 0; i < len(rest); {
		nl := strings.IndexByte(rest[i:], '\n')
		var line string
		if nl < 0 {
			line = rest[i:]
		} else {
			line = rest[i : i+nl]
		}
		if strings.TrimRight(line, "\r") == "---" {
			if nl < 0 {
				return ""
			}
			return strings.TrimLeft(rest[i+nl+1:], "\n")
		}
		if nl < 0 {
			break
		}
		i += nl + 1
	}
	return md // no closing fence — treat the whole thing as body
}

// apiExportFormats reports which export formats this host can currently produce
// (PDF depends on a headless renderer being on PATH). The UI greys out PDF when
// it's false.
// GET /api/v1/kb/export/formats → 200 {"html":true,"docx":true,"pdf":false}
func (s *Server) apiExportFormats(c echo.Context) error {
	return c.JSON(http.StatusOK, export.AvailableFormats())
}

// mdImageRefRE matches a markdown IMAGE (leading "!") whose destination has no
// URL scheme and no title/space — a candidate vault-relative image reference
// like ![alt](assets/pic.png). Group 1 is the alt text, group 2 the destination.
// Only images are inlined: goldmark permits a data: URI in an <img src> but
// deliberately blanks one in an <a href> (a security property this export path
// keeps), so a non-image file attachment stays a portable relative link instead.
var mdImageRefRE = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)\)`)

// maxInlineAssetBytes caps how large a single asset may be to inline into an
// export. base64 inflates by ~33%, so this keeps a self-contained HTML/PDF from
// ballooning without bound; a larger asset is left as its original reference.
const maxInlineAssetBytes = 10 << 20 // 10 MiB

// inlineVaultAssets rewrites vault-relative IMAGE references in a note's
// markdown (e.g. ![](assets/pic.png)) into self-contained data: URIs, reading
// the bytes from the workspace's vault, so an exported HTML/PDF carries its
// embedded images instead of dangling relative references that resolve to
// nothing once downloaded. A destination that carries a URL scheme, is
// absolute, is a fragment, isn't an image, can't be read from the vault, or is
// too large is left untouched. (File-attachment LINKS keep their portable
// relative path — goldmark blanks a data: href, so a link can't be inlined
// without weakening this path's HTML sanitization.)
func (s *Server) inlineVaultAssets(workspaceID string, md []byte) []byte {
	if s.vault == nil {
		return md
	}
	return mdImageRefRE.ReplaceAllFunc(md, func(m []byte) []byte {
		sub := mdImageRefRE.FindSubmatch(m)
		if sub == nil {
			return m
		}
		alt, dest := string(sub[1]), string(sub[2])
		// Skip anything that isn't a plain vault-relative path: schemes
		// (http:, https:, data:), protocol-relative, absolute paths, fragments.
		if strings.Contains(dest, "://") || strings.HasPrefix(dest, "//") ||
			strings.HasPrefix(dest, "/") || strings.HasPrefix(dest, "#") ||
			strings.HasPrefix(dest, "data:") {
			return m
		}
		raw, err := s.vault.ReadNote(workspaceID, dest)
		if err != nil || len(raw) == 0 || len(raw) > maxInlineAssetBytes {
			return m
		}
		ct := mime.TypeByExtension(path.Ext(dest))
		if ct == "" {
			ct = http.DetectContentType(raw)
		}
		// Only inline genuine images (goldmark renders these safely; a
		// non-image data: URI in an <img> would just be a broken image).
		if !strings.HasPrefix(ct, "image/") {
			return m
		}
		encoded := base64.StdEncoding.EncodeToString(raw)
		return []byte(fmt.Sprintf("![%s](data:%s;base64,%s)", alt, ct, encoded))
	})
}

// apiExportKBNote renders a markdown note to HTML, DOCX, or PDF and streams it
// as a download. Export is note-only (a non-.md file is 400) and is the
// sanctioned reverse of internal/convert.
// GET /api/v1/kb/export?path=<rel>&format=html|docx|pdf → attachment
func (s *Server) apiExportKBNote(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	rel := cleanKBParam(c.QueryParam("path"))
	if rel == "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_path", "a note path is required")
	}
	if !strings.EqualFold(path.Ext(rel), ".md") {
		return jsonErr(c, http.StatusBadRequest, "not_a_note", "only markdown notes can be exported")
	}
	format := strings.ToLower(strings.TrimSpace(c.QueryParam("format")))

	data, err := s.vault.ReadNote(u.ID, rel)
	if err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "could not open note: "+err.Error())
	}
	stem := strings.TrimSuffix(path.Base(rel), path.Ext(rel))
	body := []byte(stripFrontmatter(string(data)))
	opts := export.Options{Title: stem}

	var (
		out         []byte
		contentType string
		ext         string
	)
	switch format {
	case "html":
		out, err = export.ToHTML(s.inlineVaultAssets(u.ID, body), opts)
		contentType, ext = "text/html; charset=utf-8", "html"
	case "docx":
		// DOCX degrades images to alt-text by design (internal/export/docx.go),
		// so inlining data URIs would only bloat it (or worse, turn a link into
		// a giant data: hyperlink target) — export the note as-is.
		out, err = export.ToDOCX(body, opts)
		contentType, ext = "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "docx"
	case "pdf":
		out, err = export.ToPDF(s.inlineVaultAssets(u.ID, body), opts)
		contentType, ext = "application/pdf", "pdf"
		if errors.Is(err, export.ErrNoPDFEngine) {
			return jsonErr(c, http.StatusUnprocessableEntity, "pdf_unavailable",
				"PDF export needs a headless renderer on the server (weasyprint, chromium, wkhtmltopdf, libreoffice, or pandoc). Install one, or export HTML/Word instead.")
		}
	default:
		return jsonErr(c, http.StatusBadRequest, "invalid_format", "format must be one of: html, docx, pdf")
	}
	if err != nil {
		slog.Error("kb export failed", "workspace_id", u.ID, "path", rel, "format", format, "err", err)
		return jsonErr(c, http.StatusInternalServerError, "export_failed", "could not export this note")
	}

	filename := stem + "." + ext
	c.Response().Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	return c.Blob(http.StatusOK, contentType, out)
}

// isRequestTooLarge reports whether err originates from an http.MaxBytesReader
// tripping (Go 1.19+ wraps this as *http.MaxBytesError). Checked so a body
// that overflows the cap during multipart parsing itself — not just a big
// fh.Size after the fact — is still reported as 413, not a generic 400.
func isRequestTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

// uploadErrStatus maps an ImportFile error to an API status+code+client-safe
// message. Unlike vaultErrStatus (which only ever sees vault.ErrEscapes /
// os.ErrNotExist from a path-safety check), ImportFile can fail for either a
// property of the REQUEST (unconvertible format; a destination that resolves
// into a system-managed area or escapes the vault) or a genuine SERVER fault
// (a disk I/O error while preserving the original or writing the note) — the
// two must not collapse to the same 422, or a real fault gets reported to
// the user as "we can't read this kind of file". The 500 branch's message is
// generic on purpose: the real error (which can contain a raw filesystem
// path) is for the server log only, via the caller.
func uploadErrStatus(err error) (status int, code string, clientMsg string) {
	switch {
	case errors.Is(err, convert.ErrUnsupportedFormat):
		return http.StatusUnprocessableEntity, "unsupported_format", err.Error()
	case errors.Is(err, vault.ErrSystemDir), errors.Is(err, vault.ErrEscapes):
		return http.StatusUnprocessableEntity, "invalid_destination", err.Error()
	default:
		return http.StatusInternalServerError, "internal_error", "something went wrong saving this file"
	}
}

// apiUploadKBFile accepts a document, converts it to markdown, and files it in
// the workspace's knowledge base. It shares vault.ImportFile with the
// save_to_kb LLM tool and the CLI bridge, so a file lands identically no
// matter which door it came through.
// POST /api/v1/kb/upload multipart {file, dir?} → 200 {note_path, original_path, kind, extractor, warnings}
func (s *Server) apiUploadKBFile(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}

	// Cap the request body at the io.Reader level, BEFORE any multipart
	// parsing reads it into memory: FormFile/ParseMultipartForm reads the
	// whole body regardless of what fh.Size later reports, so checking
	// fh.Size alone would still let an oversized body be read in full first.
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxUploadBytes+maxUploadOverhead)

	fh, err := c.FormFile("file")
	if err != nil {
		if isRequestTooLarge(err) {
			return jsonErr(c, http.StatusRequestEntityTooLarge, "too_large",
				fmt.Sprintf("upload exceeds the %d byte limit", maxUploadBytes))
		}
		return jsonErr(c, http.StatusBadRequest, "invalid_request", "no file uploaded")
	}
	if fh.Size > maxUploadBytes {
		return jsonErr(c, http.StatusRequestEntityTooLarge, "too_large",
			fmt.Sprintf("file is %d bytes; the limit is %d", fh.Size, maxUploadBytes))
	}
	src, err := fh.Open()
	if err != nil {
		return jsonErr(c, http.StatusBadRequest, "invalid_request", "could not read the upload")
	}
	defer src.Close()
	// Belt-and-braces re-check against the actual bytes read: reading one byte
	// past the cap turns any fh.Size lie into a hard stop rather than a
	// trusted header value.
	data, err := iolimit.ReadCapped(src, maxUploadBytes)
	if err != nil {
		if errors.Is(err, iolimit.ErrTooLarge) {
			return jsonErr(c, http.StatusRequestEntityTooLarge, "too_large",
				fmt.Sprintf("file exceeds the %d byte limit", maxUploadBytes))
		}
		return jsonErr(c, http.StatusBadRequest, "invalid_request", "could not read the upload")
	}

	res, err := s.vault.ImportFile(u.ID, vault.ImportInput{
		Data:       data,
		Filename:   fh.Filename,
		DestDir:    strings.TrimSpace(c.FormValue("dir")),
		BuildPhase: false,
	})
	if err != nil {
		status, code, msg := uploadErrStatus(err)
		if status == http.StatusInternalServerError {
			// The raw error can carry a filesystem path (e.g. "write note:
			// open /home/.../vaults/...: permission denied") — log it for the
			// operator but never hand it to the client.
			slog.Error("kb upload: import failed", "workspace_id", u.ID, "err", err)
		}
		return jsonErr(c, status, code, msg)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"note_path":     res.NotePath,
		"original_path": res.OriginalPath,
		"kind":          res.Kind,
		"extractor":     res.Extractor,
		"warnings":      orEmpty(res.Warnings),
	})
}
