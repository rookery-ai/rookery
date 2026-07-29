package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LocalDestination stores snapshots in a directory on the host. It is the
// reference implementation of Destination and makes the whole engine testable
// with no network.
type LocalDestination struct {
	dir string
}

func NewLocalDestination(dir string) *LocalDestination {
	return &LocalDestination{dir: dir}
}

func (d *LocalDestination) Name() string { return "local:" + d.dir }

// Put writes to a temp file and renames, so a listing never shows a
// half-written snapshot.
func (d *LocalDestination) Put(ctx context.Context, name string, r io.Reader, size int64) error {
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	tmp := filepath.Join(d.dir, name+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, filepath.Join(d.dir, name)); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("finalize %s: %w", name, err)
	}
	return nil
}

func (d *LocalDestination) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	f, err := os.Open(filepath.Join(d.dir, name))
	if err != nil {
		return nil, fmt.Errorf("open snapshot %s: %w", name, err)
	}
	return f, nil
}

func (d *LocalDestination) List(ctx context.Context) ([]Entry, error) {
	items, err := os.ReadDir(d.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list backup dir: %w", err)
	}
	var out []Entry
	for _, it := range items {
		if it.IsDir() || !IsSnapshotName(it.Name()) {
			continue
		}
		info, err := it.Info()
		if err != nil {
			continue
		}
		out = append(out, Entry{Name: it.Name(), Size: info.Size(), ModTime: info.ModTime()})
	}
	return out, nil
}

func (d *LocalDestination) Delete(ctx context.Context, name string) error {
	if !IsSnapshotName(name) {
		return fmt.Errorf("refusing to delete %q: not a snapshot name", name)
	}
	if err := os.Remove(filepath.Join(d.dir, name)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete %s: %w", name, err)
	}
	return nil
}
