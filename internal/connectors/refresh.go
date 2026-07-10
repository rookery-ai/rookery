package connectors

import (
	"context"
	"log/slog"
	"time"
)

// refreshCutoff is how far ahead of expiry the background loop proactively refreshes.
const refreshCutoff = 10 * time.Minute

// refreshDue refreshes every ACTIVE connection expiring within refreshCutoff and
// returns how many were refreshed (used by tests). The DB query already selected the
// near-expiry rows, so it force-refreshes each directly rather than going through
// AccessToken (whose tighter expiry skew would skip a token 3–10 min from expiry).
func refreshDue(ctx context.Context, store *DBTokenStore) int {
	cutoff := store.now().Add(refreshCutoff).UTC().Format(time.RFC3339)
	rows, err := store.DB.ConnectionsNearExpiry(ctx, cutoff)
	if err != nil {
		slog.Warn("connectors refresh: query failed", "err", err)
		return 0
	}
	n := 0
	for i := range rows {
		if _, err := store.refresh(ctx, &rows[i]); err != nil {
			slog.Warn("connectors refresh: failed", "conn", rows[i].ID, "err", err)
			continue
		}
		n++
	}
	return n
}

// RunRefreshLoop periodically refreshes soon-to-expire connection tokens until ctx is
// done. Start it as a goroutine in serve.
func RunRefreshLoop(ctx context.Context, store *DBTokenStore, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			refreshDue(ctx, store)
		}
	}
}
