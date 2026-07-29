package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// archiveFile pairs an on-disk source with the name it takes inside the archive.
type archiveFile struct {
	Name string // slash-separated path within the archive
	Path string // absolute path on disk
}

// skipDirNames are never archived. claude-homes is excluded because it is
// hundreds of megabytes of regenerable coder cache whose .credentials.json is
// re-copied from the operator's ~/.claude on every invocation.
var skipDirNames = map[string]bool{
	".restore-staging": true,
	"claude-homes":     true,
}

func isSkippedDir(name string) bool {
	return skipDirNames[name] ||
		strings.HasPrefix(name, ".pre-restore-") ||
		strings.HasPrefix(name, ".backup-work-")
}

// collectVaultFiles walks every workspace vault with a raw WalkDir.
//
// It deliberately does NOT use vault.List or its siblings: those hide dotfiles
// by design, which would silently omit .kb/ (the db-export sidecars and
// links.json) from every snapshot. The archive wants the literal tree; the KB
// browser's helpers exist to hide things from humans.
func collectVaultFiles(vaultsDir string) ([]archiveFile, error) {
	if _, err := os.Stat(vaultsDir); os.IsNotExist(err) {
		return nil, nil // an install with no workspaces yet
	}
	var out []archiveFile
	err := filepath.WalkDir(vaultsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if isSkippedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks, sockets, devices
		}
		rel, err := filepath.Rel(vaultsDir, p)
		if err != nil {
			return err
		}
		out = append(out, archiveFile{
			Name: path.Join("vaults", filepath.ToSlash(rel)),
			Path: p,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk vaults: %w", err)
	}
	return out, nil
}

// writeArchive streams files into dst as tar+gzip, prefixed by the manifest.
// Checksums are computed in a first pass so the manifest — which must come
// first, for the compatibility gate — can carry them.
func writeArchive(dst io.Writer, files []archiveFile, m Manifest) error {
	m.FormatVersion = FormatVersion
	m.Files = make([]FileEntry, 0, len(files))
	m.TotalBytes = 0

	for _, f := range files {
		sum, size, err := hashFile(f.Path)
		if err != nil {
			return err
		}
		m.Files = append(m.Files, FileEntry{Path: f.Name, Size: size, SHA256: sum})
		m.TotalBytes += size
	}

	gz := gzip.NewWriter(dst)
	tw := tar.NewWriter(gz)

	manifestJSON, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: ManifestName, Mode: 0o600, Size: int64(len(manifestJSON)),
		ModTime: m.CreatedAt, Typeflag: tar.TypeReg,
	}); err != nil {
		return fmt.Errorf("write manifest header: %w", err)
	}
	if _, err := tw.Write(manifestJSON); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	for i, f := range files {
		if err := copyIntoTar(tw, f, m.Files[i].Size, m.CreatedAt); err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar: %w", err)
	}
	return gz.Close()
}

func copyIntoTar(tw *tar.Writer, f archiveFile, size int64, modTime time.Time) error {
	in, err := os.Open(f.Path)
	if err != nil {
		return fmt.Errorf("open %s: %w", f.Path, err)
	}
	defer in.Close()

	if err := tw.WriteHeader(&tar.Header{
		Name: f.Name, Mode: 0o600, Size: size, ModTime: modTime, Typeflag: tar.TypeReg,
	}); err != nil {
		return fmt.Errorf("write header %s: %w", f.Name, err)
	}
	// io.CopyN, not io.Copy: tar demands exactly the declared size, and a file
	// that grew between hashing and copying would otherwise corrupt the stream.
	if _, err := io.CopyN(tw, in, size); err != nil {
		return fmt.Errorf("copy %s: %w", f.Name, err)
	}
	return nil
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, fmt.Errorf("open %s: %w", p, err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("hash %s: %w", p, err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// readArchive extracts src into destDir and returns the manifest, verifying
// every file's SHA-256 against it. A mismatch aborts, naming the file.
//
// The manifest is also written to destDir, because ApplyPendingRestore reads
// the system key back out of the staged copy.
func readArchive(src io.Reader, destDir string) (*Manifest, error) {
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return nil, fmt.Errorf("create destination: %w", err)
	}
	gz, err := gzip.NewReader(src)
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	tr := tar.NewReader(gz)

	var m *Manifest
	want := map[string]FileEntry{}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		if hdr.Name == ManifestName {
			raw, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read manifest: %w", err)
			}
			m = &Manifest{}
			if err := json.Unmarshal(raw, m); err != nil {
				return nil, fmt.Errorf("parse manifest: %w", err)
			}
			for _, e := range m.Files {
				want[e.Path] = e
			}
			if err := os.WriteFile(filepath.Join(destDir, ManifestName), raw, 0o600); err != nil {
				return nil, fmt.Errorf("write manifest: %w", err)
			}
			continue
		}
		if m == nil {
			return nil, fmt.Errorf("archive does not start with %s", ManifestName)
		}

		target, err := safeJoin(destDir, hdr.Name)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, fmt.Errorf("create dir for %s: %w", hdr.Name, err)
		}
		sum, n, err := writeAndHash(target, tr)
		if err != nil {
			return nil, err
		}
		entry, ok := want[hdr.Name]
		if !ok {
			return nil, fmt.Errorf("archive contains %s which the manifest does not list", hdr.Name)
		}
		if entry.SHA256 != sum || entry.Size != n {
			return nil, fmt.Errorf("checksum mismatch for %s", hdr.Name)
		}
		delete(want, hdr.Name)
	}

	// Drain to the end of the gzip stream so its CRC32 trailer is actually
	// verified. tar stops at its own end-of-archive marker, which sits BEFORE
	// the gzip trailer — without this, damage to the tail of the stream goes
	// entirely undetected. (Per-file SHA-256 still catches damage to file
	// contents; this covers the rest.)
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return nil, fmt.Errorf("verify archive integrity: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("close archive: %w", err)
	}

	if m == nil {
		return nil, fmt.Errorf("archive has no %s", ManifestName)
	}
	for name := range want {
		return nil, fmt.Errorf("manifest lists %s but the archive does not contain it", name)
	}
	return m, nil
}

func writeAndHash(target string, r io.Reader) (string, int64, error) {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", 0, fmt.Errorf("create %s: %w", target, err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		return "", 0, fmt.Errorf("write %s: %w", target, err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// safeJoin refuses any archive member whose name escapes destDir. Extracting an
// untrusted tar without this is the classic zip-slip vulnerability.
func safeJoin(destDir, name string) (string, error) {
	if path.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("archive member %q is an absolute path", name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	target := filepath.Join(destDir, clean)
	rel, err := filepath.Rel(destDir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive member %q escapes the destination", name)
	}
	return target, nil
}
