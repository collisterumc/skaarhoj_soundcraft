package main

import (
	"math"
	"strconv"
	"strings"

	pb "github.com/SKAARHOJ/ibeam-corelib-go/ibeam-core"
	b "github.com/SKAARHOJ/ibeam-corelib-go/paramhelpers"
	log "github.com/s00500/env_logger"
)

// Wire mapping for the mute/fader milestone (IMPLEMENTATION.md §2.4). Wire
// indices are 0-based; the channel dimension is 1-based. Dimension index
// 1..inputs maps to i.<n-1>; inputs+1.. maps to l.<n-inputs-1>. The mixer never
// validates values, so the core clamps floats to [0,1] and sends booleans as
// exactly 0 or 1 (Decision 2026-08-23).

// buildWireMessages translates one fromManager parameter into wire lines.
func (d *device) buildWireMessages(param *pb.Parameter) []string {
	switch param.Id.Parameter {
	case d.pids.snapshotUp:
		return d.snapshotStepMessages(1)
	case d.pids.snapshotDown:
		return d.snapshotStepMessages(-1)
	}

	var msgs []string
	for _, v := range param.Value {
		path, ok := d.wirePath(param.Id.Parameter, v.DimensionID)
		if !ok {
			continue
		}
		switch param.Id.Parameter {
		case d.pids.channelMute:
			msgs = append(msgs, "SETD^"+path+".mute^"+boolWire(v.GetBinary()))
		case d.pids.channelFader:
			msgs = append(msgs, "SETD^"+path+".mix^"+floatWire(v.GetFloating()))
		case d.pids.masterFader:
			msgs = append(msgs, "SETD^"+path+".mix^"+floatWire(v.GetFloating()))
		}
	}
	if len(msgs) == 0 {
		log.Debugf("Device %d: no wire mapping for parameter %d", d.id, param.Id.Parameter)
	}
	return msgs
}

// snapshotStepMessages resolves a snapshot up/down trigger to a single
// LOADSNAPSHOT line. delta is +1 for next, -1 for previous. An empty cache or a
// current snapshot outside the cached list is a logged no-op, so a trigger with
// nothing to load sends nothing (IMPLEMENTATION.md §4).
func (d *device) snapshotStepMessages(delta int) []string {
	show, items := d.snapshots.snapshot()
	if len(items) == 0 {
		log.Debugf("Device %d: snapshot step ignored, no cached snapshot list for show %q", d.id, show)
		return nil
	}
	current, _ := d.store.get("var.currentSnapshot")
	target, ok := stepSnapshot(items, current, delta)
	if !ok {
		log.Debugf("Device %d: snapshot step ignored, current snapshot %q not in cached list", d.id, current)
		return nil
	}
	return []string{"LOADSNAPSHOT^" + show + "^" + target}
}

// wirePath resolves the channel path stem (e.g. "i.2", "l.0", "m") for a
// parameter and its dimension. ok is false for parameters with no wire mapping.
func (d *device) wirePath(pid uint32, dimensionID []uint32) (string, bool) {
	switch pid {
	case d.pids.masterFader:
		return "m", true
	case d.pids.channelMute, d.pids.channelFader:
		if len(dimensionID) != 1 || dimensionID[0] < 1 || int(dimensionID[0]) > d.inputs+d.line {
			log.Errorf("Device %d: parameter %d has invalid channel dimension %v", d.id, pid, dimensionID)
			return "", false
		}
		return channelPath(int(dimensionID[0]), d.inputs)
	}
	return "", false
}

// channelPath maps a 1-based channel dimension index to its 0-based wire stem.
// Indices 1..inputs are input channels (i); the rest are line-in (l).
func channelPath(dim, inputs int) (string, bool) {
	if dim <= inputs {
		return "i." + strconv.Itoa(dim-1), true
	}
	return "l." + strconv.Itoa(dim-inputs-1), true
}

// confirmWrite returns the optimistic-confirm parameter for a fromManager
// write: the sent value fed straight back as current. The mixer never echoes a
// write, so waiting for feedback would leave the value assumed forever
// (Decision 2026-08-23). Returns nil for parameters with no wire mapping.
func (d *device) confirmWrite(param *pb.Parameter) *pb.Parameter {
	switch param.Id.Parameter {
	case d.pids.channelMute, d.pids.channelFader, d.pids.masterFader:
	default:
		return nil
	}
	vals := make([]*pb.ParameterValue, 0, len(param.Value))
	for _, v := range param.Value {
		switch param.Id.Parameter {
		case d.pids.channelMute:
			vals = append(vals, b.Bool(v.GetBinary(), v.DimensionID...))
		case d.pids.channelFader, d.pids.masterFader:
			vals = append(vals, b.Float(clampUnit(v.GetFloating()), v.DimensionID...))
		}
	}
	if len(vals) == 0 {
		return nil
	}
	return b.Param(param.Id.Parameter, d.id, vals...)
}

// inboundParameter maps a recognized SETD path to a toManager current value.
// It handles m.mix and {i|l}.<n>.{mute|mix}; every other path returns nil so
// the store keeps it but nothing forwards it.
func (d *device) inboundParameter(path, value string) *pb.Parameter {
	if path == "m.mix" {
		f, ok := parseFloat(value)
		if !ok {
			return nil
		}
		return b.Param(d.pids.masterFader, d.id, b.Float(clampUnit(f)))
	}
	// The display shows the snapshot name alone: a small panel has one line, and
	// the operator steps snapshots, not shows. var.currentSnapshot covers both
	// the initial dump and mixer-side loads (both arrive as this SETS).
	if path == "var.currentSnapshot" {
		return b.Param(d.pids.currentSnapshot, d.id, b.String(value))
	}

	typ, rest, ok := strings.Cut(path, ".")
	if !ok || (typ != "i" && typ != "l") {
		return nil
	}
	idxStr, prop, ok := strings.Cut(rest, ".")
	if !ok {
		return nil
	}
	wireIdx, err := strconv.Atoi(idxStr)
	if err != nil || wireIdx < 0 {
		return nil
	}
	dim := d.channelDim(typ, wireIdx)
	if dim == 0 {
		return nil
	}

	switch prop {
	case "mute":
		on, ok := parseBool(value)
		if !ok {
			return nil
		}
		return b.Param(d.pids.channelMute, d.id, b.Bool(on, dim))
	case "mix":
		f, ok := parseFloat(value)
		if !ok {
			return nil
		}
		return b.Param(d.pids.channelFader, d.id, b.Float(clampUnit(f), dim))
	}
	return nil
}

// channelDim maps a wire type and 0-based index back to the 1-based channel
// dimension. Returns 0 for an index outside the configured range.
func (d *device) channelDim(typ string, wireIdx int) uint32 {
	switch typ {
	case "i":
		if wireIdx >= d.inputs {
			return 0
		}
		return uint32(wireIdx + 1)
	case "l":
		if wireIdx >= d.line {
			return 0
		}
		return uint32(d.inputs + wireIdx + 1)
	}
	return 0
}

// parseFloat accepts the mixer's numeric SETD values; empty, non-numeric, and
// non-finite (NaN/Inf) are rejected so they never propagate as current values.
func parseFloat(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// parseBool reads a mixer boolean. The mixer stores non-0/1 numbers verbatim
// (§9), so treat any nonzero numeric as true and reject non-numeric.
func parseBool(s string) (bool, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) {
		return false, false
	}
	return f != 0, true
}

// boolWire renders a boolean as the exact 0/1 the mixer expects.
func boolWire(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// floatWire clamps to [0,1] and formats as a plain shortest decimal (no
// exponent, no trailing zeros). This matches outbound-messages.spec.ts for
// every in-range value and, unlike Go's 'g' format, never emits exponent
// notation for tiny faders (e.g. 3e-05) that the mixer would ignore.
func floatWire(v float64) string {
	return strconv.FormatFloat(clampUnit(v), 'f', -1, 64)
}

// clampUnit clamps to [0,1] and maps a non-finite value to 0, so NaN can never
// reach the wire (neither the mixer nor corelib rejects "NaN"). v <= 0 also
// normalizes -0.0, which would otherwise render as "-0".
func clampUnit(v float64) float64 {
	if math.IsNaN(v) || v <= 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
