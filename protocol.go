package main

import "strings"

// Soundcraft Ui wire codec. The framing is a Socket.IO-0.9 remnant: logical
// messages travel prefixed "3:::"; "1::"/"2::" frames are handshake/heartbeat
// noise. Validated on Ui16 hardware — IMPLEMENTATION.md §2.1, §9.

const framePrefix = "3:::"

// encodeFrame wraps one logical message for the wire.
func encodeFrame(msg string) string {
	return framePrefix + msg
}

// decodeFrame strips the framing and splits a batch frame into logical lines.
// Non-data frames return ok=false and must be ignored.
func decodeFrame(frame string) (lines []string, ok bool) {
	payload, found := strings.CutPrefix(frame, framePrefix)
	if !found {
		return nil, false
	}
	return strings.Split(payload, "\n"), true
}

// message is one parsed protocol line. SETD/SETS carry path and value; every
// other kind (commands, list replies, BMSG) keeps its raw args.
type message struct {
	kind  string   // "SETD", "SETS", "SHOWLIST", "SNAPSHOTLIST", "BMSG", …
	path  string   // SETD/SETS only
	value string   // SETD/SETS only; may itself contain '^'
	args  []string // non-set messages; nil when the line is bare
}

func parseMessage(line string) (message, bool) {
	if line == "" {
		return message{}, false
	}
	kind, rest, hasArgs := strings.Cut(line, "^")
	switch kind {
	case "SETD", "SETS":
		// The path sits between the first two separators; the value is the
		// remainder and may itself contain '^' (IMPLEMENTATION.md §2.2).
		path, value, found := strings.Cut(rest, "^")
		if !found || path == "" {
			return message{}, false
		}
		return message{kind: kind, path: path, value: value}, true
	default:
		var args []string
		if hasArgs {
			args = strings.Split(rest, "^")
		}
		return message{kind: kind, args: args}, true
	}
}
