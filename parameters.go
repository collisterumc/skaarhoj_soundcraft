package main

import (
	"fmt"

	ib "github.com/SKAARHOJ/ibeam-corelib-go"
	pb "github.com/SKAARHOJ/ibeam-corelib-go/ibeam-core"
	b "github.com/SKAARHOJ/ibeam-corelib-go/paramhelpers"
)

// modelSpec holds per-model registration data and channel counts
// (from soundcraft-ui's device capabilities). The counts size the channel dimensions of the
// mute/fader parameters in later milestones.
type modelSpec struct {
	id          uint32
	name        string
	description string
	inputs      int // `i` channels
	line        int // `l` channels
	player      int // `p` channels
	fx          int // `f` channels
	sub         int // `s` channels
	aux         int // `a` channels
	vca         int // `v` channels
	multitrack  bool
}

var models = []modelSpec{
	{id: 1, name: "Ui12", description: "Soundcraft Ui12 digital mixer (8 inputs)",
		inputs: 8, line: 2, player: 2, fx: 4, sub: 4, aux: 4},
	{id: 2, name: "Ui16", description: "Soundcraft Ui16 digital mixer (12 inputs)",
		inputs: 12, line: 2, player: 2, fx: 4, sub: 4, aux: 6},
	{id: 3, name: "Ui24R", description: "Soundcraft Ui24R digital mixer (24 inputs, multitrack)",
		inputs: 24, line: 2, player: 2, fx: 4, sub: 6, aux: 10, vca: 6, multitrack: true},
}

// channelCounts is the input/line split a device needs to map the channel
// dimension to and from wire paths.
type channelCounts struct {
	inputs int
	line   int
}

// modelChannels returns the input and line-in counts for a registered model ID.
// The channel dimension splits input from line-in at the input boundary.
// Unknown IDs return zeros, so an unmapped device maps no channels rather than
// panicking.
func modelChannels(modelID uint32) channelCounts {
	for _, m := range models {
		if m.id == modelID {
			return channelCounts{inputs: m.inputs, line: m.line}
		}
	}
	return channelCounts{}
}

func registerModels(r *ib.IBeamParameterRegistry) {
	for _, m := range models {
		r.RegisterModel(&pb.ModelInfo{
			Id:              m.id,
			Name:            m.name,
			Description:     m.description,
			DeviceWebUILink: "http://{ip}/",
		})
	}
}

func configureParameters(r *ib.IBeamParameterRegistry) {
	// Registered explicitly (rather than relying on the corelib auto-add) so the
	// device loop can report it by name. No other parameter sets
	// controllableWhileDisconnected, so Reactor blocks outputs while the mixer is off.
	r.RegisterParameter(&pb.ParameterDetail{
		Id:            &pb.ModelParameterID{Parameter: 1},
		Path:          "config",
		Name:          "connection",
		Label:         "Connected",
		ShortLabel:    "Connected",
		Description:   "Connection status of the mixer",
		GenericType:   pb.GenericType_ConnectionState,
		ControlStyle:  pb.ControlStyle_NoControl,
		FeedbackStyle: pb.FeedbackStyle_NormalFeedback,
		ValueType:     pb.ValueType_Binary,
		DefaultValue:  b.Bool(false),
	}, ib.WithDefaultValid())

	// channel_mute and channel_fader share a per-model channel dimension sized
	// inputs + line-in (G0 decision). RegisterParameterForModels registers one
	// detail per model, so each model gets its own dimension size under the same
	// parameter name — PID("channel_mute") resolves the same on all models.
	for _, m := range models {
		dim := channelDimension(m)
		r.RegisterParameterForModels([]uint32{m.id}, &pb.ParameterDetail{
			Path:          "mix",
			Name:          "channel_mute",
			Label:         "Channel Mute",
			ShortLabel:    "Mute",
			Description:   "Master-mix mute for input and line-in channels",
			ControlStyle:  pb.ControlStyle_Normal,
			FeedbackStyle: pb.FeedbackStyle_NormalFeedback,
			ValueType:     pb.ValueType_Binary,
			DefaultValue:  b.Bool(false),
			// Optimistic confirm handles the missing echo; retry guards a lost
			// write, which the mixer never validates or acknowledges.
			RetryCount:     2,
			ControlDelayMs: 50,
			Dimensions:     []*pb.DimensionDetail{dim},
		})

		// channel_fader_db is the read-only dB companion to channel_fader: the
		// display shows dB while the tick moves on the linear 0–100 scale. It shares
		// channel_fader's per-model channel dimension so each channel pairs with its
		// own reading. Registered before channel_fader references it.
		r.RegisterParameterForModels([]uint32{m.id}, &pb.ParameterDetail{
			Path:          "mix",
			Name:          "channel_fader_db",
			Label:         "Channel Fader (dB)",
			ShortLabel:    "Fader dB",
			Description:   "Channel master-mix fader reading in dB (read-only display of channel_fader)",
			ControlStyle:  pb.ControlStyle_NoControl,
			FeedbackStyle: pb.FeedbackStyle_NormalFeedback,
			ValueType:     pb.ValueType_String,
			DefaultValue:  b.String(""),
			Dimensions:    []*pb.DimensionDetail{dim},
		}, ib.WithDefaultValid())

		r.RegisterParameterForModels([]uint32{m.id}, &pb.ParameterDetail{
			Path:          "mix",
			Name:          "channel_fader",
			Label:         "Channel Fader",
			ShortLabel:    "Fader",
			Description:   "Master-mix fader level for input and line-in channels (0–100, linear tick)",
			ControlStyle:  pb.ControlStyle_Normal,
			FeedbackStyle: pb.FeedbackStyle_NormalFeedback,
			ValueType:     pb.ValueType_Floating,
			Minimum:       0,
			Maximum:       100,
			// Other clients push floats at ~9 decimals; on the 0–100 scale a matching
			// push lands within 0.1, so the threshold clears our assumed state.
			AcceptanceThreshold: 0.1,
			FineSteps:           0.5,
			CoarseSteps:         5,
			// One decimal because the tick moves on the linear 0–100 scale.
			DisplayFloatPrecision:          pb.FloatPrecision_OneDecimal,
			RecommendedParamForTextDisplay: "channel_fader_db",
			RetryCount:                     2,
			ControlDelayMs:                 50,
			Dimensions:                     []*pb.DimensionDetail{dim},
		})
	}

	// current_snapshot is a read-only display of the mixer's active snapshot name,
	// fed from SETS^var.currentSnapshot. Registered before the up/down triggers so
	// their RecommendedParamForTextDisplay can reference it at validation time.
	r.RegisterParameter(&pb.ParameterDetail{
		Path:          "snapshot",
		Name:          "current_snapshot",
		Label:         "Current Snapshot",
		ShortLabel:    "Snapshot",
		Description:   "Name of the mixer's active snapshot in the current show",
		ControlStyle:  pb.ControlStyle_NoControl,
		FeedbackStyle: pb.FeedbackStyle_NormalFeedback,
		ValueType:     pb.ValueType_String,
		DefaultValue:  b.String(""),
	}, ib.WithDefaultValid())

	// snapshot_up / snapshot_down step to the adjacent snapshot in the current
	// show's cached list (wrapping at the ends). Oneshot triggers carry no value;
	// corelib delivers exactly one Trigger per press to the device loop.
	for _, name := range []struct{ pname, label, short, desc string }{
		{"snapshot_up", "Next Snapshot", "Snap +", "Load the next snapshot in the current show"},
		{"snapshot_down", "Previous Snapshot", "Snap -", "Load the previous snapshot in the current show"},
	} {
		r.RegisterParameter(&pb.ParameterDetail{
			Path:                           "snapshot",
			Name:                           name.pname,
			Label:                          name.label,
			ShortLabel:                     name.short,
			Description:                    name.desc,
			ControlStyle:                   pb.ControlStyle_Oneshot,
			FeedbackStyle:                  pb.FeedbackStyle_NoFeedback,
			ValueType:                      pb.ValueType_NoValue,
			RecommendedParamForTextDisplay: "current_snapshot",
		})
	}

	// master_fader_db is the read-only dB companion to master_fader. Registered
	// before master_fader references it.
	r.RegisterParameter(&pb.ParameterDetail{
		Path:          "mix",
		Name:          "master_fader_db",
		Label:         "Master Fader (dB)",
		ShortLabel:    "Master dB",
		Description:   "Master output fader reading in dB (read-only display of master_fader)",
		ControlStyle:  pb.ControlStyle_NoControl,
		FeedbackStyle: pb.FeedbackStyle_NormalFeedback,
		ValueType:     pb.ValueType_String,
		DefaultValue:  b.String(""),
	}, ib.WithDefaultValid())

	// master_fader has no dimension; it maps to the single m.mix path.
	r.RegisterParameter(&pb.ParameterDetail{
		Path:                "mix",
		Name:                "master_fader",
		Label:               "Master Fader",
		ShortLabel:          "Master",
		Description:         "Master output fader level (0–100, linear tick). The mixer exposes no master mute path.",
		ControlStyle:        pb.ControlStyle_Normal,
		FeedbackStyle:       pb.FeedbackStyle_NormalFeedback,
		ValueType:           pb.ValueType_Floating,
		Minimum:             0,
		Maximum:             100,
		AcceptanceThreshold: 0.1,
		FineSteps:           0.5,
		CoarseSteps:         5,
		// One decimal because the tick moves on the linear 0–100 scale.
		DisplayFloatPrecision:          pb.FloatPrecision_OneDecimal,
		RecommendedParamForTextDisplay: "master_fader_db",
		RetryCount:                     2,
		ControlDelayMs:                 50,
	})

	configureRecordingParameters(r)
}

// configureRecordingParameters registers the 2-track recorder parameters (all
// models) and the Ui24R-only multitrack parameters.
func configureRecordingParameters(r *ib.IBeamParameterRegistry) {
	// record_2track is a toggle whose button shows the actual var.isRecording
	// state. The core never optimistically confirms it — that would lie during
	// the ~206 ms the mixer takes to report the new state — so ControlDelayMs
	// must exceed that latency: at 300 ms the confirm beats the first corelib
	// retry and a normal press fires once, with no MaxRetrys error. RetryCount 0
	// is rejected by corelib validation for Normal+NormalFeedback. QuarantineDelayMs
	// stays 0: a nonzero value makes corelib reject an external stop/start inside
	// the window, re-fighting a tablet operator. recordGuard suppresses
	// re-presses and any retry that does fire.
	r.RegisterParameter(&pb.ParameterDetail{
		Path:           "record",
		Name:           "record_2track",
		Label:          "Record",
		ShortLabel:     "Rec",
		Description:    "Start/stop the 2-track USB recording (toggle; shows the mixer's actual recording state)",
		ControlStyle:   pb.ControlStyle_Normal,
		FeedbackStyle:  pb.FeedbackStyle_NormalFeedback,
		ValueType:      pb.ValueType_Binary,
		DefaultValue:   b.Bool(false),
		RetryCount:     2,
		ControlDelayMs: 300,
	}, ib.WithDefaultValid())

	// record_busy mirrors var.recBusy for display only. Nothing gates on it: on
	// hardware it never fires on start and pulses ~76 ms on stop, so it cannot
	// cover the ~206 ms command-to-state race (measured on hardware).
	r.RegisterParameter(&pb.ParameterDetail{
		Path:          "record",
		Name:          "record_busy",
		Label:         "Recording Busy",
		ShortLabel:    "Busy",
		Description:   "Mixer 2-track recording busy flag (read-only; does not cover the start/stop race)",
		ControlStyle:  pb.ControlStyle_NoControl,
		FeedbackStyle: pb.FeedbackStyle_NormalFeedback,
		ValueType:     pb.ValueType_Binary,
		DefaultValue:  b.Bool(false),
	}, ib.WithDefaultValid())

	// Multitrack recording is Ui24R-only. Gate on the model, never on
	// var.mtk.present: a Ui16 falsely reports it as 1 (measured on hardware).
	// These are implemented from spec and untested on hardware (no Ui24R on hand).
	ui24r := ui24rModelIDs()
	if len(ui24r) == 0 {
		return
	}

	r.RegisterParameterForModels(ui24r, &pb.ParameterDetail{
		Path:          "record",
		Name:          "record_multitrack",
		Label:         "Multitrack Record",
		ShortLabel:    "MTK Rec",
		Description:   "Start/stop the Ui24R multitrack recording (toggle; untested on hardware)",
		ControlStyle:  pb.ControlStyle_Normal,
		FeedbackStyle: pb.FeedbackStyle_NormalFeedback,
		ValueType:     pb.ValueType_Binary,
		DefaultValue:  b.Bool(false),
		// var.mtk.rec.currentState is a plain 0/1 boolean in soundcraft-ui
		// (selectBoolean), not the MtkState player enum, so it maps directly.
		// Timing mirrors record_2track (QuarantineDelayMs left 0); the mtk confirm
		// latency is untested on hardware.
		RetryCount:     2,
		ControlDelayMs: 300,
	}, ib.WithDefaultValid())

	r.RegisterParameterForModels(ui24r, &pb.ParameterDetail{
		Path:          "record",
		Name:          "multitrack_busy",
		Label:         "Multitrack Busy",
		ShortLabel:    "MTK Busy",
		Description:   "Ui24R multitrack recording busy flag (read-only; untested on hardware)",
		ControlStyle:  pb.ControlStyle_NoControl,
		FeedbackStyle: pb.FeedbackStyle_NormalFeedback,
		ValueType:     pb.ValueType_Binary,
		DefaultValue:  b.Bool(false),
	}, ib.WithDefaultValid())

	r.RegisterParameterForModels(ui24r, &pb.ParameterDetail{
		Path:          "record",
		Name:          "multitrack_time",
		Label:         "Multitrack Time",
		ShortLabel:    "MTK Time",
		Description:   "Ui24R multitrack recording elapsed time (read-only; untested on hardware)",
		ControlStyle:  pb.ControlStyle_NoControl,
		FeedbackStyle: pb.FeedbackStyle_NormalFeedback,
		ValueType:     pb.ValueType_String,
		DefaultValue:  b.String(""),
	}, ib.WithDefaultValid())
}

// ui24rModelIDs lists the model IDs that carry a multitrack recorder.
func ui24rModelIDs() []uint32 {
	var ids []uint32
	for _, m := range models {
		if m.multitrack {
			ids = append(ids, m.id)
		}
	}
	return ids
}

// channelDimension builds the 1-based channel dimension for a model: inputs
// first, then line-in, labelled "IN n" / "LINE n". Element IDs run 1..count so
// they align with the wire-index mapping in wireMessages (dimension index 1..N
// → i.0..; the remainder → l.0..).
func channelDimension(m modelSpec) *pb.DimensionDetail {
	count := m.inputs + m.line
	labels := make(map[uint32]string, count)
	for i := 0; i < m.inputs; i++ {
		labels[uint32(i+1)] = fmt.Sprintf("IN %d", i+1)
	}
	for i := 0; i < m.line; i++ {
		labels[uint32(m.inputs+i+1)] = fmt.Sprintf("LINE %d", i+1)
	}
	return &pb.DimensionDetail{
		Name:          "channel",
		Count:         uint32(count),
		Description:   "Input and line-in channels",
		ElementLabels: labels,
	}
}
