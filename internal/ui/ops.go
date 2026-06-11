package ui

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Op is a long-running background operation shown in the header's activity
// panel (scaling a database, purging a queue, ...).
type Op struct {
	ID      int64
	Label   string
	Started time.Time
}

var (
	opsMu    sync.Mutex
	ops      = map[int64]Op{}
	nextOpID atomic.Int64
)

// BeginOp registers a background operation for the activity panel. Call it
// from the Update handler that launches the work, and defer EndOp inside the
// command so the entry disappears when the work finishes.
func BeginOp(format string, args ...any) int64 {
	id := nextOpID.Add(1)
	opsMu.Lock()
	ops[id] = Op{ID: id, Label: fmt.Sprintf(format, args...), Started: time.Now()}
	opsMu.Unlock()
	return id
}

func EndOp(id int64) {
	opsMu.Lock()
	delete(ops, id)
	opsMu.Unlock()
}

// Ops returns active operations, oldest first.
func Ops() []Op {
	opsMu.Lock()
	defer opsMu.Unlock()
	out := make([]Op, 0, len(ops))
	for _, o := range ops {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}
