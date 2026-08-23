package main

import "testing"

func TestStateStore(t *testing.T) {
	s := newStateStore()

	if _, ok := s.get("i.0.mute"); ok {
		t.Error("empty store returned a value")
	}

	s.set("i.0.mute", "1")
	s.set("var.currentShow", "Training")
	s.set("i.0.mute", "0") // overwrite

	if v, ok := s.get("i.0.mute"); !ok || v != "0" {
		t.Errorf("get(i.0.mute) = %q, %v; want \"0\", true", v, ok)
	}
	if s.size() != 2 {
		t.Errorf("size = %d, want 2", s.size())
	}

	s.clear()
	if s.size() != 0 {
		t.Errorf("size after clear = %d, want 0", s.size())
	}
	if _, ok := s.get("var.currentShow"); ok {
		t.Error("cleared store returned a value")
	}
}
