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
		id:                    1,
		inputs:                12, // Ui16
		line:                  2,
		store:                 newStateStore(),
		snapshots:             newSnapshotCache(),
		record2trackGuard:     newRecordGuard(recordGuardTimeout),
		recordMultitrackGuard: newRecordGuard(recordGuardTimeout),
		pids: mixerPIDs{
			channelMute:      r.PID("channel_mute"),
			channelFader:     r.PID("channel_fader"),
			channelFaderDB:   r.PID("channel_fader_db"),
			masterFader:      r.PID("master_fader"),
			masterFaderDB:    r.PID("master_fader_db"),
			snapshotUp:       r.PID("snapshot_up"),
			snapshotDown:     r.PID("snapshot_down"),
			currentSnapshot:  r.PID("current_snapshot"),
			record2track:     r.PID("record_2track"),
			recordBusy:       r.PID("record_busy"),
			recordMultitrack: r.PIDByModel("record_multitrack", 3),
			multitrackBusy:   r.PIDByModel("multitrack_busy", 3),
			multitrackTime:   r.PIDByModel("multitrack_time", 3),
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
		// Fader inputs are on the 0–100 scale; the wire value is that /100.
		{"input 3 mute on", b.Param(d.pids.channelMute, d.id, b.Bool(true, 3)), "SETD^i.2.mute^1"},
		{"input 3 mute off", b.Param(d.pids.channelMute, d.id, b.Bool(false, 3)), "SETD^i.2.mute^0"},
		{"input 3 fader 40", b.Param(d.pids.channelFader, d.id, b.Float(40, 3)), "SETD^i.2.mix^0.4"},
		{"input 3 fader clamp high", b.Param(d.pids.channelFader, d.id, b.Float(120, 3)), "SETD^i.2.mix^1"},
		{"input 3 fader clamp low", b.Param(d.pids.channelFader, d.id, b.Float(-4, 3)), "SETD^i.2.mix^0"},
		// A dB-derived exact wire string is covered by TestOutboundDBFader. The old
		// -12 dB literal is dropped here: dividing an arbitrary 0–100 value by 100
		// can lengthen the wire decimal (49.333689429/100 = 0.49333689429000005),
		// which the mixer accepts but no longer matches the reference string exactly.
		// line(1) → dimension index inputs+1 = 13 → l.0.
		{"line 1 fader 40", b.Param(d.pids.channelFader, d.id, b.Float(40, 13)), "SETD^l.0.mix^0.4"},
		{"line 1 mute on", b.Param(d.pids.channelMute, d.id, b.Bool(true, 13)), "SETD^l.0.mute^1"},
		{"line 1 mute off", b.Param(d.pids.channelMute, d.id, b.Bool(false, 13)), "SETD^l.0.mute^0"},
		{"master fader 80", b.Param(d.pids.masterFader, d.id, b.Float(80)), "SETD^m.mix^0.8"},
		{"master fader clamp high", b.Param(d.pids.masterFader, d.id, b.Float(120)), "SETD^m.mix^1"},
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
	pos := dbToFaderValue(3) * 100 // fader scale is 0–100; wire value is that /100
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
		{"i.2.mix", "0.4", d.pids.channelFader, []uint32{3}, func(v *pb.ParameterValue) bool { return v.GetFloating() == 40 }},
		{"l.0.mute", "1", d.pids.channelMute, []uint32{13}, func(v *pb.ParameterValue) bool { return v.GetBinary() }},
		{"l.1.mix", "0.5", d.pids.channelFader, []uint32{14}, func(v *pb.ParameterValue) bool { return v.GetFloating() == 50 }},
		{"m.mix", "0.7", d.pids.masterFader, nil, func(v *pb.ParameterValue) bool { return v.GetFloating() == 70 }},
	}
	for _, c := range cases {
		ps := d.inboundParameter(c.path, c.value)
		p := paramByPID(ps, c.wantPID)
		if p == nil {
			t.Errorf("%s^%s: no parameter for PID %d produced", c.path, c.value, c.wantPID)
			continue
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

// paramByPID returns the first parameter in ps matching pid, or nil.
func paramByPID(ps []*pb.Parameter, pid uint32) *pb.Parameter {
	for _, p := range ps {
		if p.Id.Parameter == pid {
			return p
		}
	}
	return nil
}

// TestInboundFaderDBCompanion checks a fader path also emits its dB companion
// with the same dimension and the formatted reading. Wire 0.5 → -11.6 dB;
// wire 1.0 → 10.0 dB; negligible amplitude → "-inf dB".
func TestInboundFaderDBCompanion(t *testing.T) {
	d := wireTestDevice(t)

	cases := []struct {
		path, value string
		fPID, dbPID uint32
		wantDim     []uint32
		wantFader   float64
		wantDB      string
	}{
		{"i.2.mix", "0.5", d.pids.channelFader, d.pids.channelFaderDB, []uint32{3}, 50, "-11.6 dB"},
		{"m.mix", "1.0", d.pids.masterFader, d.pids.masterFaderDB, nil, 100, "10.0 dB"},
		{"l.0.mix", "0", d.pids.channelFader, d.pids.channelFaderDB, []uint32{13}, 0, "-inf dB"},
	}
	for _, c := range cases {
		ps := d.inboundParameter(c.path, c.value)
		if len(ps) != 2 {
			t.Fatalf("%s^%s: want 2 parameters (fader+db), got %d", c.path, c.value, len(ps))
		}
		f := paramByPID(ps, c.fPID)
		db := paramByPID(ps, c.dbPID)
		if f == nil || db == nil {
			t.Fatalf("%s^%s: missing fader or db parameter (%v)", c.path, c.value, ps)
		}
		if f.Value[0].GetFloating() != c.wantFader {
			t.Errorf("%s^%s: fader = %v, want %v", c.path, c.value, f.Value[0].GetFloating(), c.wantFader)
		}
		if !dimEqual(f.Value[0].DimensionID, c.wantDim) || !dimEqual(db.Value[0].DimensionID, c.wantDim) {
			t.Errorf("%s^%s: dimension mismatch fader=%v db=%v want=%v", c.path, c.value, f.Value[0].DimensionID, db.Value[0].DimensionID, c.wantDim)
		}
		if got := db.Value[0].GetStr(); got != c.wantDB {
			t.Errorf("%s^%s: db text = %q, want %q", c.path, c.value, got, c.wantDB)
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
		if ps := d.inboundParameter(path, "1"); len(ps) != 0 {
			t.Errorf("%s: expected no parameter, got %d", path, len(ps))
		}
	}
	// Non-numeric and non-finite values are rejected (the mixer ignores them too).
	for _, v := range []string{"", "abc", "NaN", "Inf"} {
		if ps := d.inboundParameter("i.0.mix", v); len(ps) != 0 {
			t.Errorf("mix value %q should be rejected", v)
		}
	}
}

// TestConfirmWriteFaderDBCompanion checks the optimistic confirm feeds back the
// 0–100 fader value and its paired dB reading, clamping out-of-range inputs.
func TestConfirmWriteFaderDBCompanion(t *testing.T) {
	d := wireTestDevice(t)

	cases := []struct {
		name        string
		param       *pb.Parameter
		fPID, dbPID uint32
		wantDim     []uint32
		wantFader   float64
		wantDB      string
	}{
		// wire 0.5 (fader 50) → -11.6 dB
		{"channel 50", b.Param(d.pids.channelFader, d.id, b.Float(50, 3)), d.pids.channelFader, d.pids.channelFaderDB, []uint32{3}, 50, "-11.6 dB"},
		// wire 1.0 (fader 100) → 10.0 dB
		{"master 100", b.Param(d.pids.masterFader, d.id, b.Float(100)), d.pids.masterFader, d.pids.masterFaderDB, nil, 100, "10.0 dB"},
		// clamp high: fader 120 → 100 → 10.0 dB
		{"channel clamp high", b.Param(d.pids.channelFader, d.id, b.Float(120, 5)), d.pids.channelFader, d.pids.channelFaderDB, []uint32{5}, 100, "10.0 dB"},
		// clamp low: fader -4 → 0 → -inf dB
		{"master clamp low", b.Param(d.pids.masterFader, d.id, b.Float(-4)), d.pids.masterFader, d.pids.masterFaderDB, nil, 0, "-inf dB"},
	}
	for _, c := range cases {
		ps := d.confirmWrite(c.param)
		if len(ps) != 2 {
			t.Fatalf("%s: want 2 confirm parameters (fader+db), got %d", c.name, len(ps))
		}
		f := paramByPID(ps, c.fPID)
		db := paramByPID(ps, c.dbPID)
		if f == nil || db == nil {
			t.Fatalf("%s: missing fader or db parameter (%v)", c.name, ps)
		}
		if f.Value[0].GetFloating() != c.wantFader {
			t.Errorf("%s: fader = %v, want %v", c.name, f.Value[0].GetFloating(), c.wantFader)
		}
		if !dimEqual(f.Value[0].DimensionID, c.wantDim) || !dimEqual(db.Value[0].DimensionID, c.wantDim) {
			t.Errorf("%s: dimension mismatch fader=%v db=%v want=%v", c.name, f.Value[0].DimensionID, db.Value[0].DimensionID, c.wantDim)
		}
		if got := db.Value[0].GetStr(); got != c.wantDB {
			t.Errorf("%s: db text = %q, want %q", c.name, got, c.wantDB)
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

// TestRecordInboundMapping checks the recording state keys map to the right
// parameters, including the Ui24R-only multitrack keys (wireTestDevice resolves
// their PIDs via model 3).
func TestRecordInboundMapping(t *testing.T) {
	d := wireTestDevice(t)

	cases := []struct {
		path, value string
		wantPID     uint32
		checkBool   *bool
		checkStr    string
	}{
		{"var.isRecording", "1", d.pids.record2track, boolp(true), ""},
		{"var.isRecording", "0", d.pids.record2track, boolp(false), ""},
		{"var.recBusy", "1", d.pids.recordBusy, boolp(true), ""},
		{"var.mtk.rec.currentState", "1", d.pids.recordMultitrack, boolp(true), ""},
		{"var.mtk.rec.busy", "1", d.pids.multitrackBusy, boolp(true), ""},
		{"var.mtk.rec.time", "00:23", d.pids.multitrackTime, nil, "00:23"},
	}
	for _, c := range cases {
		p := paramByPID(d.inboundParameter(c.path, c.value), c.wantPID)
		if p == nil {
			t.Errorf("%s^%s: no parameter for PID %d produced", c.path, c.value, c.wantPID)
			continue
		}
		if c.checkBool != nil && p.Value[0].GetBinary() != *c.checkBool {
			t.Errorf("%s^%s: bool %v, want %v", c.path, c.value, p.Value[0].GetBinary(), *c.checkBool)
		}
		if c.checkStr != "" && p.Value[0].GetStr() != c.checkStr {
			t.Errorf("%s^%s: string %q, want %q", c.path, c.value, p.Value[0].GetStr(), c.checkStr)
		}
	}
}

// TestRecordToggleWireConstruction covers both toggle commands: with no observed
// state yet, a first press sends the bare toggle command exactly once.
func TestRecordToggleWireConstruction(t *testing.T) {
	d := wireTestDevice(t)

	got := d.buildWireMessages(b.Param(d.pids.record2track, d.id, b.Bool(true)))
	if len(got) != 1 || got[0] != "RECTOGGLE" {
		t.Errorf("record_2track start → %v, want [RECTOGGLE]", got)
	}

	got = d.buildWireMessages(b.Param(d.pids.recordMultitrack, d.id, b.Bool(true)))
	if len(got) != 1 || got[0] != "MTK_REC_TOGGLE" {
		t.Errorf("record_multitrack start → %v, want [MTK_REC_TOGGLE]", got)
	}
}

// TestRecordToggleStateGated proves the observed recording state gates the send:
// a target already matching the mixer's reported state sends nothing.
func TestRecordToggleStateGated(t *testing.T) {
	d := wireTestDevice(t)
	d.record2trackGuard.observe(true) // mixer reports recording

	if got := d.buildWireMessages(b.Param(d.pids.record2track, d.id, b.Bool(true))); len(got) != 0 {
		t.Errorf("target==current should send nothing, got %v", got)
	}
	// A differing target sends one RECTOGGLE.
	if got := d.buildWireMessages(b.Param(d.pids.record2track, d.id, b.Bool(false))); len(got) != 1 || got[0] != "RECTOGGLE" {
		t.Errorf("target!=current → %v, want [RECTOGGLE]", got)
	}
}

// TestRecordConfirmWriteSkipped confirms record toggles are never optimistically
// confirmed: the mixer broadcasts var.isRecording back to the sender, so a local
// confirm would lie during the ~206 ms transition.
func TestRecordConfirmWriteSkipped(t *testing.T) {
	d := wireTestDevice(t)
	if ps := d.confirmWrite(b.Param(d.pids.record2track, d.id, b.Bool(true))); len(ps) != 0 {
		t.Errorf("record_2track must not be optimistically confirmed, got %+v", ps)
	}
	if ps := d.confirmWrite(b.Param(d.pids.recordMultitrack, d.id, b.Bool(true))); len(ps) != 0 {
		t.Errorf("record_multitrack must not be optimistically confirmed, got %+v", ps)
	}
}

// TestMultitrackModelGating asserts the multitrack parameters exist only for the
// Ui24R model. This is the strongest unit-level check corelib allows via
// GetParameterDetail; the live gRPC ParameterDetail listing (Gate G6) must still
// confirm the same absence over the wire.
func TestMultitrackModelGating(t *testing.T) {
	r := testRegistry(t)

	for _, name := range []string{"record_multitrack", "multitrack_busy", "multitrack_time"} {
		pid := r.PID(name) // global name→ID; nonzero once registered for any model
		if pid == 0 {
			t.Fatalf("%s not registered for any model", name)
		}
		// Present on Ui24R (model 3).
		if _, err := r.GetParameterDetail(pid, 3); err != nil {
			t.Errorf("%s missing for Ui24R: %v", name, err)
		}
		// Absent on Ui12 (1) and Ui16 (2).
		for _, modelID := range []uint32{1, 2} {
			if _, err := r.GetParameterDetail(pid, modelID); err == nil {
				t.Errorf("%s must be absent for model %d", name, modelID)
			}
		}
	}

	// The 2-track parameters exist on every model.
	for _, name := range []string{"record_2track", "record_busy"} {
		pid := r.PID(name)
		for _, modelID := range []uint32{1, 2, 3} {
			if _, err := r.GetParameterDetail(pid, modelID); err != nil {
				t.Errorf("%s missing for model %d: %v", name, modelID, err)
			}
		}
	}
}

func boolp(v bool) *bool { return &v }

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
