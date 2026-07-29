package backup

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// Prune deletes all but the newest keep snapshots and returns what it removed.
//
// It lists through the Destination, which already filters on the snapshot name
// pattern, and filters again here. A destination is frequently a bucket or
// folder holding other things, and deleting a stranger's file would be an
// unrecoverable bug in a feature whose entire purpose is not losing data.
func Prune(ctx context.Context, d Destination, keep int) ([]string, error) {
	if keep < 1 {
		return nil, errors.New("backup: retention must keep at least one snapshot")
	}
	entries, err := d.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("backup: list for retention: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if IsSnapshotName(e.Name) {
			names = append(names, e.Name)
		}
	}
	// Snapshot names embed a sortable UTC timestamp, so lexical order is
	// chronological order.
	sort.Strings(names)
	if len(names) <= keep {
		return nil, nil
	}

	var deleted []string
	for _, name := range names[:len(names)-keep] {
		if err := d.Delete(ctx, name); err != nil {
			return deleted, fmt.Errorf("backup: delete %s: %w", name, err)
		}
		deleted = append(deleted, name)
	}
	return deleted, nil
}
