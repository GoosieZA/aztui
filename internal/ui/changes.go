package ui

import (
	"sync"
	"time"
)

// Change is one successful mutation made through aztui in this session.
type Change struct {
	At     time.Time
	Scope  string // resource the change applied to
	Action string
}

var (
	changesMu sync.Mutex
	changes   []Change
)

const maxChanges = 50

// RecordChange logs a successful mutation for the session-changes feed on
// the home screen. In-memory only — it resets when aztui exits.
func RecordChange(scope, action string) {
	changesMu.Lock()
	defer changesMu.Unlock()
	changes = append([]Change{{At: time.Now(), Scope: scope, Action: action}}, changes...)
	if len(changes) > maxChanges {
		changes = changes[:maxChanges]
	}
}

// Changes returns the session's mutations, newest first.
func Changes() []Change {
	changesMu.Lock()
	defer changesMu.Unlock()
	out := make([]Change, len(changes))
	copy(out, changes)
	return out
}
