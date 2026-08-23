package main

import (
	"reflect"
	"testing"
)

func TestEncodeFrame(t *testing.T) {
	got := encodeFrame("SETD^i.0.mute^1")
	want := "3:::SETD^i.0.mute^1"
	if got != want {
		t.Errorf("encodeFrame = %q, want %q", got, want)
	}
	if encodeFrame("ALIVE") != "3:::ALIVE" {
		t.Errorf("encodeFrame(ALIVE) = %q", encodeFrame("ALIVE"))
	}
}

func TestDecodeFrame(t *testing.T) {
	tests := []struct {
		name  string
		frame string
		want  []string
		ok    bool
	}{
		{"single line", "3:::SETD^i.0.mute^1", []string{"SETD^i.0.mute^1"}, true},
		{"batch", "3:::SETD^a^1\nSETS^b^x\nRTA^abc", []string{"SETD^a^1", "SETS^b^x", "RTA^abc"}, true},
		{"heartbeat 2", "2::", nil, false},
		{"handshake 1", "1::", nil, false},
		{"empty", "", nil, false},
		{"bare prefix", "3:::", []string{""}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := decodeFrame(tt.frame)
			if ok != tt.ok || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("decodeFrame(%q) = %v, %v; want %v, %v", tt.frame, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestParseMessage(t *testing.T) {
	tests := []struct {
		name string
		line string
		want message
		ok   bool
	}{
		{"SETD bool", "SETD^i.0.mute^1", message{kind: "SETD", path: "i.0.mute", value: "1"}, true},
		{"SETD float", "SETD^i.0.mix^0.09659714599", message{kind: "SETD", path: "i.0.mix", value: "0.09659714599"}, true},
		{"SETD var", "SETD^var.isRecording^0", message{kind: "SETD", path: "var.isRecording", value: "0"}, true},
		{"SETS", "SETS^var.currentShow^Training", message{kind: "SETS", path: "var.currentShow", value: "Training"}, true},
		{"SETS value with separator", "SETS^i.0.name^Guitar^Lead", message{kind: "SETS", path: "i.0.name", value: "Guitar^Lead"}, true},
		{"SETS empty value", "SETS^i.0.name^", message{kind: "SETS", path: "i.0.name", value: ""}, true},
		{"SETD missing value", "SETD^i.0.mute", message{}, false},
		{"SETD bare", "SETD", message{}, false},
		{"empty line", "", message{}, false},
		{"flat list", "SHOWLIST^2023-10-19^Default^Training", message{kind: "SHOWLIST", args: []string{"2023-10-19", "Default", "Training"}}, true},
		{"keyed list", "SNAPSHOTLIST^Training^2024-03-17^2026-08-22", message{kind: "SNAPSHOTLIST", args: []string{"Training", "2024-03-17", "2026-08-22"}}, true},
		{"empty keyed list", "SNAPSHOTLIST^2023-10-19^", message{kind: "SNAPSHOTLIST", args: []string{"2023-10-19", ""}}, true},
		{"BMSG sync", "BMSG^SYNC^abc^1", message{kind: "BMSG", args: []string{"SYNC", "abc", "1"}}, true},
		{"bare command reply", "MSG^$SNAPLOAD^snap", message{kind: "MSG", args: []string{"$SNAPLOAD", "snap"}}, true},
		{"argless line", "RTA", message{kind: "RTA"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseMessage(tt.line)
			if ok != tt.ok || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseMessage(%q) = %+v, %v; want %+v, %v", tt.line, got, ok, tt.want, tt.ok)
			}
		})
	}
}
