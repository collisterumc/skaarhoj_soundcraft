package main

import (
	"sync"
	"time"
)

// recordGuard emits exactly one toggle command per user intent. Recording is
// driven by toggle-only wire commands (RECTOGGLE, MTK_REC_TOGGLE): the core
// sends one only when the target differs from the mixer's current recording
// state, and the mixer reports the new state only ~206 ms later (measured on
// hardware). A re-press or a corelib retry inside that window would emit a
// second toggle and undo the first. The guard swallows those until the reported
// state reaches the target, or a timeout backstops a state that never arrives
// (e.g. no USB stick).
//
// The guard owns the last-known recording state, updated by observe from the
// reader goroutine. tryToggle decides and arms under the same lock, so there is
// no read-then-decide race between the store and the guard.
type recordGuard struct {
	mu       sync.Mutex
	armed    bool
	target   bool
	armedAt  time.Time
	current  bool // last state reported by the mixer
	knowsCur bool // false until the first observation

	timeout time.Duration
	now     func() time.Time // test seam
}

func newRecordGuard(timeout time.Duration) *recordGuard {
	return &recordGuard{timeout: timeout, now: time.Now}
}

// tryToggle decides whether a toggle to target should send a wire command, using
// the guard's own last-known state. It returns true only when a command must go
// out; that call also arms the guard. A target already matching the known state,
// or a retry arriving inside the in-flight window, returns false. Until the first
// observation the state is unknown; a toggle then sends, letting the first press
// start recording even before the dump (which carries var.isRecording) lands.
func (g *recordGuard) tryToggle(target bool) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.armed {
		// An in-flight command is pending. Let it complete unless it has timed
		// out (the state never arrived) — then a fresh intent may send again.
		if g.now().Sub(g.armedAt) < g.timeout {
			return false
		}
		g.armed = false
	}

	if g.knowsCur && target == g.current {
		return false
	}

	g.armed = true
	g.target = target
	g.armedAt = g.now()
	return true
}

// observe records the mixer's current recording state and clears the guard once
// that state reaches the target the guard sent, so an opposite intent can send
// again without waiting out the timeout.
func (g *recordGuard) observe(current bool) {
	g.mu.Lock()
	g.current = current
	g.knowsCur = true
	if g.armed && current == g.target {
		g.armed = false
	}
	g.mu.Unlock()
}

// clear resets the guard, including its knowledge of the mixer state. Called on
// disconnect so no in-flight or stale state survives a power cycle.
func (g *recordGuard) clear() {
	g.mu.Lock()
	g.armed = false
	g.knowsCur = false
	g.current = false
	g.mu.Unlock()
}
