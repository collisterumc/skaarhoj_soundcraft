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

// listReply is a parsed list-reply message (IMPLEMENTATION.md §2.2). Flat lists
// (SHOWLIST) carry only items and an empty key. Keyed lists (SNAPSHOTLIST^show)
// carry the key plus items. The empty form is a trailing separator
// (SNAPSHOTLIST^show^ or SHOWLIST^) and yields no items. List fields are
// separator-split whole; unlike SETS values they never contain '^'.
type listReply struct {
	key   string   // the show for a keyed list; empty for a flat list
	items []string // list entries in wire order; empty for the empty form
}

// parseListReply reads a list-reply message into a key and items. keyed selects
// how the first field is read: keyed lists (SNAPSHOTLIST) treat it as the show
// key, flat lists (SHOWLIST) treat it as the first item. ok is false when the
// message carries no fields at all.
func parseListReply(msg message, keyed bool) (listReply, bool) {
	if len(msg.args) == 0 {
		return listReply{}, false
	}
	fields := msg.args
	var key string
	if keyed {
		key = fields[0]
		fields = fields[1:]
	}
	// The empty form is a lone trailing separator: strings.Split leaves one
	// empty field, which is not a real entry.
	if len(fields) == 1 && fields[0] == "" {
		fields = nil
	}
	return listReply{key: key, items: fields}, true
}
