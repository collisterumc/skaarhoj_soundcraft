package main

import "sync"

// stateStore mirrors the mixer's SETD/SETS state as a flat path→value map.
// It is cleared on disconnect so no stale state survives a power cycle
// (IMPLEMENTATION.md §5).
type stateStore struct {
	mu sync.RWMutex
	m  map[string]string
}

func newStateStore() *stateStore {
	return &stateStore{m: make(map[string]string)}
}

func (s *stateStore) set(path, value string) {
	s.mu.Lock()
	s.m[path] = value
	s.mu.Unlock()
}

func (s *stateStore) get(path string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[path]
	return v, ok
}

func (s *stateStore) size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.m)
}

func (s *stateStore) clear() {
	s.mu.Lock()
	s.m = make(map[string]string)
	s.mu.Unlock()
}
