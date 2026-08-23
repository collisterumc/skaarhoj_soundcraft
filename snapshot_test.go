package main

import (
	"reflect"
	"testing"
)

func TestParseListReply(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		keyed bool
		want  listReply
		ok    bool
	}{
		{"flat multi", "SHOWLIST^ShowA^ShowB^ShowC", false,
			listReply{key: "", items: []string{"ShowA", "ShowB", "ShowC"}}, true},
		{"flat single", "SHOWLIST^ShowA", false,
			listReply{key: "", items: []string{"ShowA"}}, true},
		{"flat empty (trailing separator)", "SHOWLIST^", false,
			listReply{key: "", items: nil}, true},
		{"keyed multi", "SNAPSHOTLIST^ShowA^Snap1^Snap2", true,
			listReply{key: "ShowA", items: []string{"Snap1", "Snap2"}}, true},
		{"keyed single", "SNAPSHOTLIST^ShowA^Snap1", true,
			listReply{key: "ShowA", items: []string{"Snap1"}}, true},
		{"keyed empty (trailing separator)", "SNAPSHOTLIST^ShowA^", true,
			listReply{key: "ShowA", items: nil}, true},
		{"CUELIST empty form", "CUELIST^Default^", true,
			listReply{key: "Default", items: nil}, true},
		{"bare command, no fields", "SHOWLIST", false, listReply{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok := parseMessage(tt.line)
			if !ok {
				t.Fatalf("parseMessage(%q) failed", tt.line)
			}
			got, ok := parseListReply(msg, tt.keyed)
			if ok != tt.ok || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseListReply(%q, keyed=%v) = %+v, %v; want %+v, %v",
					tt.line, tt.keyed, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestStepSnapshot(t *testing.T) {
	list := []string{"Snap1", "Snap2", "Snap3"}
	tests := []struct {
		name    string
		items   []string
		current string
		delta   int
		want    string
		ok      bool
	}{
		{"next adjacent", list, "Snap1", 1, "Snap2", true},
		{"prev adjacent", list, "Snap2", -1, "Snap1", true},
		{"next wraps at end", list, "Snap3", 1, "Snap1", true},
		{"prev wraps at start", list, "Snap1", -1, "Snap3", true},
		{"single item next wraps to self", []string{"Only"}, "Only", 1, "Only", true},
		{"single item prev wraps to self", []string{"Only"}, "Only", -1, "Only", true},
		{"empty list no-op", nil, "Snap1", 1, "", false},
		{"unknown current no-op", list, "Missing", 1, "", false},
		{"unknown current empty string no-op", list, "", -1, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := stepSnapshot(tt.items, tt.current, tt.delta)
			if got != tt.want || ok != tt.ok {
				t.Errorf("stepSnapshot(%v, %q, %d) = %q, %v; want %q, %v",
					tt.items, tt.current, tt.delta, got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestSnapshotCacheShowScoping confirms a SNAPSHOTLIST reply for a show other
// than the current one is ignored, so a late reply cannot poison the cache.
func TestSnapshotCacheShowScoping(t *testing.T) {
	c := newSnapshotCache()
	c.setShow("ShowA")
	c.setList("ShowB", []string{"Stale1", "Stale2"}) // wrong show, ignored
	if _, items := c.snapshot(); len(items) != 0 {
		t.Errorf("cache accepted a list for the wrong show: %v", items)
	}
	c.setList("ShowA", []string{"Snap1", "Snap2"})
	show, items := c.snapshot()
	if show != "ShowA" || !reflect.DeepEqual(items, []string{"Snap1", "Snap2"}) {
		t.Errorf("cache = %q %v, want ShowA [Snap1 Snap2]", show, items)
	}
	c.clear()
	if show, items := c.snapshot(); show != "" || len(items) != 0 {
		t.Errorf("cache after clear = %q %v, want empty", show, items)
	}
}
