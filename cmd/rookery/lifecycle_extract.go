package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// maxBinarySize bounds what extractBinary will hold in memory. The real binary
// is ~40 MB; this leaves room to grow while refusing an archive whose declared
// member is absurd, which is the shape a decompression bomb takes.
const maxBinarySize = 256 << 20

// extractBinary pulls the rookery executable out of a release archive.
//
// Only the member NAMED rookery (or rookery.exe) is considered, and only at the
// archive root: goreleaser puts it there alongside LICENSE and README. Matching
// by name rather than "the first executable file" is what keeps a crafted
// archive from substituting something else, and path.Base defuses a member
// called "../../rookery".
func extractBinary(data []byte, archiveName string) ([]byte, error) {
	want := map[string]bool{"rookery": true, "rookery.exe": true}

	if strings.HasSuffix(archiveName, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("open archive: %w", err)
		}
		for _, f := range zr.File {
			if !want[path.Base(f.Name)] {
				continue
			}
			if f.UncompressedSize64 > maxBinarySize {
				return nil, fmt.Errorf("archive member %s is %d bytes, refusing", f.Name, f.UncompressedSize64)
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, maxBinarySize))
		}
		return nil, fmt.Errorf("archive %s contains no rookery binary", archiveName)
	}

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if h.Typeflag != tar.TypeReg || !want[path.Base(h.Name)] {
			continue
		}
		if h.Size > maxBinarySize {
			return nil, fmt.Errorf("archive member %s is %d bytes, refusing", h.Name, h.Size)
		}
		return io.ReadAll(io.LimitReader(tr, maxBinarySize))
	}
	return nil, fmt.Errorf("archive %s contains no rookery binary", archiveName)
}

// replaceBinary swaps target for the given bytes atomically.
//
// The temporary file is created in the SAME directory, because rename is only
// atomic within one filesystem and /tmp is frequently a different one. An
// interrupted upgrade therefore leaves either the old binary or the new one on
// PATH, never a half-written file — which is also why no rollback copy is kept:
// the failure mode this produces already is "the old binary is still there".
func replaceBinary(target string, data []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".rookery-upgrade-*")
	if err != nil {
		return fmt.Errorf("write next to %s: %w", target, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Match the mode of what is being replaced, so an install that was made
	// group-readable stays that way; fall back to 0755.
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}

// isDowngrade reports whether target is an older release than current.
//
// Deliberately simple: both are goreleaser semver tags. Anything unparseable
// (a -dev build, a local build) returns false, because warning about a
// downgrade we are not sure about would train people to ignore the warning.
func isDowngrade(current, target string) bool {
	c, okc := parseSemver(current)
	t, okt := parseSemver(target)
	if !okc || !okt {
		return false
	}
	for i := 0; i < 3; i++ {
		if t[i] != c[i] {
			return t[i] < c[i]
		}
	}
	return false
}

func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
