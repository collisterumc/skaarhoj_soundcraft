package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock drives recordGuard's timing seam so the in-flight window can be
// advanced deterministically without sleeping.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newGuardWithClock(timeout time.Duration) (*recordGuard, *fakeClock) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	g := &recordGuard{timeout: timeout, now: clk.now}
	return g, clk
}

func TestRecordGuardTargetEqualsCurrent(t *testing.T) {
	g, _ := newGuardWithClock(time.Second)
	g.observe(false)
	if g.tryToggle(false) {
		t.Error("target==current should not send")
	}
	g.observe(true)
	if g.tryToggle(true) {
		t.Error("target==current should not send")
	}
}

// TestRecordGuardUnknownStateSends confirms that before any observation the first
// press sends, so the button can start recording even before the dump lands.
func TestRecordGuardUnknownStateSends(t *testing.T) {
	g, _ := newGuardWithClock(time.Second)
	if !g.tryToggle(true) {
		t.Error("unknown state should let the first toggle send")
	}
}

func TestRecordGuardSendsOnceThenSuppresses(t *testing.T) {
	g, clk := newGuardWithClock(time.Second)
	g.observe(false)

	if !g.tryToggle(true) {
		t.Fatal("first toggle to a differing target must send")
	}
	// A retry during the in-flight window is suppressed even though the observed
	// state is still stale (the mixer has not confirmed yet).
	clk.advance(50 * time.Millisecond)
	if g.tryToggle(true) {
		t.Error("retry inside the in-flight window must be suppressed")
	}
	clk.advance(100 * time.Millisecond)
	if g.tryToggle(true) {
		t.Error("second retry inside the window must be suppressed")
	}
}

func TestRecordGuardWindowExpires(t *testing.T) {
	g, clk := newGuardWithClock(time.Second)
	g.observe(false)
	if !g.tryToggle(true) {
		t.Fatal("first toggle must send")
	}
	// Past the timeout the state never arrived; a fresh intent may send again.
	clk.advance(time.Second + time.Millisecond)
	if !g.tryToggle(true) {
		t.Error("after the window expires a differing target must send again")
	}
}

func TestRecordGuardClearsOnStateMatch(t *testing.T) {
	g, clk := newGuardWithClock(time.Second)
	g.observe(false)
	if !g.tryToggle(true) {
		t.Fatal("first toggle must send")
	}
	// The mixer confirms the target; the guard clears early and records the state.
	g.observe(true)
	// An opposite intent now sends immediately, well inside the old window.
	clk.advance(10 * time.Millisecond)
	if !g.tryToggle(false) {
		t.Error("after the state matches, an opposite intent must send")
	}
}

func TestRecordGuardObserveIgnoresNonTarget(t *testing.T) {
	g, clk := newGuardWithClock(time.Second)
	g.observe(false)
	if !g.tryToggle(true) {
		t.Fatal("first toggle must send")
	}
	// A push of the pre-toggle state does not clear an in-flight guard.
	g.observe(false)
	clk.advance(10 * time.Millisecond)
	if g.tryToggle(true) {
		t.Error("a non-target observation must not clear the guard")
	}
}

// TestRecordGuardConcurrentObserve runs tryToggle concurrently with observe under
// -race to prove the guard has no data race between the decide/arm path and the
// state update. It is a race-detector exercise, not proof of the ≤1-send
// invariant — that invariant is structural (both paths take one mutex) and is
// asserted deterministically by TestRecordGuardSendsOnceThenSuppresses and
// TestRecordGuardClearsOnStateMatch. The count check below is a weak backstop.
func TestRecordGuardConcurrentObserve(t *testing.T) {
	for trial := 0; trial < 200; trial++ {
		g := newRecordGuard(time.Second)
		g.observe(false)

		var sends int32
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			// The reader confirms the target mid-flight.
			g.observe(true)
		}()
		go func() {
			defer wg.Done()
			// corelib re-fires the same target repeatedly during the window.
			for i := 0; i < 5; i++ {
				if g.tryToggle(true) {
					atomic.AddInt32(&sends, 1)
				}
			}
		}()
		wg.Wait()

		// One intent to start recording must yield at most one RECTOGGLE. A
		// second would immediately stop what the first started.
		if got := atomic.LoadInt32(&sends); got > 1 {
			t.Fatalf("trial %d: %d sends for one start intent, want ≤ 1", trial, got)
		}
	}
}

func TestRecordGuardClearResets(t *testing.T) {
	g, clk := newGuardWithClock(time.Second)
	g.observe(false)
	if !g.tryToggle(true) {
		t.Fatal("first toggle must send")
	}
	g.clear()
	// After clear (disconnect) the state is unknown again, so a fresh press sends.
	clk.advance(10 * time.Millisecond)
	if !g.tryToggle(true) {
		t.Error("after clear a differing target must send")
	}
}
