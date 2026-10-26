//go:build windows

package skilldeps

import "sync"

var envLocks sync.Map // envDir → *sync.Mutex

// lockEnv serialises installs into one environment within this process. Windows has
// no confinement for this feature at all (see the Known gaps in CLAUDE.md), so an
// in-process lock is the whole of what is offered there.
func lockEnv(envDir string) (func(), error) {
	m, _ := envLocks.LoadOrStore(envDir, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock, nil
}
