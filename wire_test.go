package main

import (
	"math"
	"testing"

	ib "github.com/SKAARHOJ/ibeam-corelib-go"
	pb "github.com/SKAARHOJ/ibeam-corelib-go/ibeam-core"
	b "github.com/SKAARHOJ/ibeam-corelib-go/paramhelpers"
)

// testRegistry registers the real models and parameters, exercising the corelib
// validation that would fatal on a bad ParameterDetail.
func testRegistry(t *testing.T) *ib.IBeamParameterRegistry {
	t.Helper()
	_, registry, _, _ := ib.CreateServer(&pb.CoreInfo{Name: "test", Label: "Test"})
	registerModels(registry)
	configureParameters(registry)
	// PID lookups require at least one registered device (Ui16 here).
	if _, err := registry.RegisterDevice(1, 2); err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	return registry
}

// wireTestDevice builds a device wired with real PIDs for the Ui16 (12 inputs).
func wireTestDevice(t *testing.T) *device {
	t.Helper()
	r := testRegistry(t)
	d := &device{
		id:     1,
		inputs: 12, // Ui16
		line:   2,
		pids: mixerPIDs{
			channelMute:  r.PID("channel_mute"),
			channelFader: r.PID("channel_fader"),
			masterFader:  r.PID("master_fader"),
		},
	}
	if d.pids.channelMute == 0 || d.pids.channelFader == 0 || d.pids.masterFader == 0 {
		t.Fatalf("parameter registration did not resolve PIDs: %+v", d.pids)
	}
	return d
}

func mustFirst(t *testing.T, msgs []string) string {
	t.Helper()
	if len(msgs) != 1 {
		t.Fatalf("want 1 wire message, got %d: %v", len(msgs), msgs)
	}
	return msgs[0]
}

// TestOutboundStrings checks path construction against the exact expected
// strings from soundcraft-ui's outbound-messages.spec.ts (Gate G4).
func TestOutboundStrings(t *testing.T) {
	d := wireTestDevice(t)

	// input(3) in the reference is 1-based; it maps to our dimension index 3
	// (i.2 on the wire).
	cases := []struct {
		name  string
		param *pb.Parameter
		want  string
	}{
		{"input 3 mute on", b.Param(d.pids.channelMute, d.id, b.Bool(true, 3)), "SETD^i.2.mute^1"},
		{"input 3 mute off", b.Param(d.pids.channelMute, d.id, b.Bool(false, 3)), "SETD^i.2.mute^0"},
		{"input 3 fader 0.4", b.Param(d.pids.channelFader, d.id, b.Float(0.4, 3)), "SETD^i.2.mix^0.4"},
		{"input 3 fader clamp high", b.Param(d.pids.channelFader, d.id, b.Float(1.2, 3)), "SETD^i.2.mix^1"},
		{"input 3 fader clamp low", b.Param(d.pids.channelFader, d.id, b.Float(-4, 3)), "SETD^i.2.mix^0"},
		{"input 3 fader -12dB", b.Param(d.pids.channelFader, d.id, b.Float(0.49333689429, 3)), "SETD^i.2.mix^0.49333689429"},
		// line(1) → dimension index inputs+1 = 13 → l.0.
		{"line 1 fader 0.4", b.Param(d.pids.channelFader, d.id, b.Float(0.4, 13)), "SETD^l.0.mix^0.4"},
		{"line 1 mute on", b.Param(d.pids.channelMute, d.id, b.Bool(true, 13)), "SETD^l.0.mute^1"},
		{"line 1 mute off", b.Param(d.pids.channelMute, d.id, b.Bool(false, 13)), "SETD^l.0.mute^0"},
		{"master fader 0.8", b.Param(d.pids.masterFader, d.id, b.Float(0.8)), "SETD^m.mix^0.8"},
		{"master fader clamp high", b.Param(d.pids.masterFader, d.id, b.Float(1.2)), "SETD^m.mix^1"},
		{"master fader clamp low", b.Param(d.pids.masterFader, d.id, b.Float(-4)), "SETD^m.mix^0"},
	}
	for _, c := range cases {
		if got := mustFirst(t, d.buildWireMessages(c.param)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestOutboundDBFader confirms the dB conversion drives the same wire string the
// reference produces for setFaderLevelDB(3) on master (0.84375627201).
func TestOutboundDBFader(t *testing.T) {
	d := wireTestDevice(t)
	pos := dbToFaderValue(3)
	got := mustFirst(t, d.buildWireMessages(b.Param(d.pids.masterFader, d.id, b.Float(pos))))
	if got != "SETD^m.mix^0.84375627201" {
		t.Errorf("master +3 dB → %q, want SETD^m.mix^0.84375627201", got)
	}
}

func TestInboundMapping(t *testing.T) {
	d := wireTestDevice(t)

	cases := []struct {
		path, value string
		wantPID     uint32
		wantDim     []uint32
		check       func(*pb.ParameterValue) bool
	}{
		{"i.0.mute", "1", d.pids.channelMute, []uint32{1}, func(v *pb.ParameterValue) bool { return v.GetBinary() }},
		{"i.11.mute", "0", d.pids.channelMute, []uint32{12}, func(v *pb.ParameterValue) bool { return !v.GetBinary() }},
		{"i.2.mix", "0.4", d.pids.channelFader, []uint32{3}, func(v *pb.ParameterValue) bool { return v.GetFloating() == 0.4 }},
		{"l.0.mute", "1", d.pids.channelMute, []uint32{13}, func(v *pb.ParameterValue) bool { return v.GetBinary() }},
		{"l.1.mix", "0.5", d.pids.channelFader, []uint32{14}, func(v *pb.ParameterValue) bool { return v.GetFloating() == 0.5 }},
		{"m.mix", "0.7", d.pids.masterFader, nil, func(v *pb.ParameterValue) bool { return v.GetFloating() == 0.7 }},
	}
	for _, c := range cases {
		p := d.inboundParameter(c.path, c.value)
		if p == nil {
			t.Errorf("%s^%s: no parameter produced", c.path, c.value)
			continue
		}
		if p.Id.Parameter != c.wantPID {
			t.Errorf("%s: PID %d, want %d", c.path, p.Id.Parameter, c.wantPID)
		}
		if len(p.Value) != 1 {
			t.Fatalf("%s: want 1 value, got %d", c.path, len(p.Value))
		}
		v := p.Value[0]
		if !dimEqual(v.DimensionID, c.wantDim) {
			t.Errorf("%s: dimension %v, want %v", c.path, v.DimensionID, c.wantDim)
		}
		if !c.check(v) {
			t.Errorf("%s^%s: value check failed (%v)", c.path, c.value, v.Value)
		}
	}
}

// TestInboundIgnoresUnmapped confirms out-of-range and unrelated paths produce
// no toManager parameter (they still land in the store elsewhere).
func TestInboundIgnoresUnmapped(t *testing.T) {
	d := wireTestDevice(t)
	for _, path := range []string{
		"i.12.mute",       // beyond 12 inputs
		"l.2.mute",        // beyond 2 line-in channels
		"i.0.pan",         // unmapped property
		"var.currentShow", // not a channel path
		"s.0.mix",         // out-of-scope channel type
		"m.dim",           // no parameter
	} {
		if p := d.inboundParameter(path, "1"); p != nil {
			t.Errorf("%s: expected no parameter, got PID %d", path, p.Id.Parameter)
		}
	}
	// Non-numeric and non-finite values are rejected (the mixer ignores them too).
	for _, v := range []string{"", "abc", "NaN", "Inf"} {
		if p := d.inboundParameter("i.0.mix", v); p != nil {
			t.Errorf("mix value %q should be rejected", v)
		}
	}
}

// TestFloatWireFormat guards against exponent notation and non-finite values
// reaching the wire; both would be values the mixer silently ignores.
func TestFloatWireFormat(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.8, "0.8"},
		{0.00003, "0.00003"},   // Go's 'g' format would emit 3e-05
		{0.000001, "0.000001"}, // Go's 'g' format would emit 1e-06
		{1.2, "1"},             // clamp high
		{-4, "0"},              // clamp low
		{math.NaN(), "0"},      // non-finite maps to 0
	}
	for _, c := range cases {
		if got := floatWire(c.in); got != c.want {
			t.Errorf("floatWire(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func dimEqual(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
