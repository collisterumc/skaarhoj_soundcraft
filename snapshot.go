package main

import "sync"

// snapshotCache holds the SNAPSHOTLIST reply for the current show. The device
// loop rebuilds it on connect and on every var.currentShow change, and clears
// it on disconnect so no stale list survives a power cycle. The mixer never
// replies to an unknown command, so the cache tolerates a
// SNAPSHOTLIST reply that never arrives: it simply stays empty and up/down
// become logged no-ops.
type snapshotCache struct {
	mu    sync.Mutex
	show  string
	items []string
}

func newSnapshotCache() *snapshotCache {
	return &snapshotCache{}
}

// setShow records the show the cache is for and drops any items from a previous
// show, so a stale list cannot be stepped after the show changes.
func (c *snapshotCache) setShow(show string) {
	c.mu.Lock()
	c.show = show
	c.items = nil
	c.mu.Unlock()
}

// setList stores the snapshot list for a show. A reply for a show other than the
// current one is ignored: it would be a late answer to a superseded request.
func (c *snapshotCache) setList(show string, items []string) {
	c.mu.Lock()
	if show == c.show {
		c.items = items
	}
	c.mu.Unlock()
}

func (c *snapshotCache) clear() {
	c.mu.Lock()
	c.show = ""
	c.items = nil
	c.mu.Unlock()
}

// snapshot returns the current show and a copy of its cached list.
func (c *snapshotCache) snapshot() (show string, items []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.show, append([]string(nil), c.items...)
}

// stepSnapshot returns the snapshot adjacent to current in items, wrapping at
// both ends. delta is +1 for next, -1 for previous. ok is false when the list
// is empty or current is not in it, so the caller logs a no-op instead of
// loading a snapshot.
func stepSnapshot(items []string, current string, delta int) (string, bool) {
	idx := -1
	for i, s := range items {
		if s == current {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", false
	}
	n := len(items)
	next := ((idx+delta)%n + n) % n
	return items[next], true
}
