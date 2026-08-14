// Package release resolves and verifies published Rookery release artifacts.
//
// The same three steps — pick a version, name the archive, check it against
// checksums.txt — exist in install.sh and install.ps1, which cannot import Go.
// This is the reference implementation, used by `rookery upgrade`;
// packaging/scripts_test.go continues to pin that the two shell installers
// build the same archive name, since that is the only way to keep three copies
// agreeing.
package release

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// Repo is the published repository.
const Repo = "rookery-ai/rookery"

// ErrNoAsset reports that a release exists but publishes nothing for this
// platform — distinct from a missing release, because the remedies differ.
type ErrNoAsset struct {
	Version, OS, Arch string
}

func (e *ErrNoAsset) Error() string {
	return fmt.Sprintf("release %s publishes no archive for %s/%s", e.Version, e.OS, e.Arch)
}

// Client is the HTTP client used for all calls. Deliberately a plain client:
// this talks to github.com, never to a user-supplied host, so the private-
// address dial guard that internal/nethttp exists for does not apply.
var Client = &http.Client{Timeout: 30 * time.Second}

// Latest returns the tag of the newest non-draft, non-prerelease release.
func Latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("check for the latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("check for the latest release: github answered %s", resp.Status)
	}
	var out struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("check for the latest release: %w", err)
	}
	if out.TagName == "" {
		return "", fmt.Errorf("check for the latest release: no tag in the response")
	}
	return out.TagName, nil
}

// ArchiveName builds goreleaser's archive name for a tag and platform.
//
// Two details are load-bearing and are pinned by packaging/scripts_test.go
// against the shell installers: the leading "v" is stripped from the tag, and
// Windows ships .zip while everything else ships .tar.gz.
func ArchiveName(version, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("rookery_%s_%s_%s.%s", strings.TrimPrefix(version, "v"), goos, goarch, ext)
}

// ArchiveURL is where that archive is published.
func ArchiveURL(version, goos, goarch string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s",
		Repo, version, ArchiveName(version, goos, goarch))
}

// ChecksumsURL is where the release's checksums.txt is published.
func ChecksumsURL(version string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/checksums.txt", Repo, version)
}

// CurrentArchiveName is ArchiveName for the running platform.
func CurrentArchiveName(version string) string {
	return ArchiveName(version, runtime.GOOS, runtime.GOARCH)
}

// ParseChecksums reads a goreleaser checksums.txt ("<sha256>  <filename>") and
// returns filename → hex digest.
func ParseChecksums(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		// goreleaser writes "*name" for binary mode in some configurations.
		out[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("checksums.txt contained no entries")
	}
	return out, nil
}

// Verify reports whether data matches want.
//
// A mismatch is a hard failure everywhere this is called. It is the only thing
// standing between a tampered or truncated download and an executable the user
// is about to run as themselves.
func Verify(data []byte, want string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch: archive hashes to %s, release publishes %s", got, want)
	}
	return nil
}
