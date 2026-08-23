package main

import (
	ib "github.com/SKAARHOJ/ibeam-corelib-go"
	pb "github.com/SKAARHOJ/ibeam-corelib-go/ibeam-core"
	b "github.com/SKAARHOJ/ibeam-corelib-go/paramhelpers"
)

// modelSpec holds per-model registration data and channel counts
// (IMPLEMENTATION.md §2.6). The counts size the channel dimensions of the
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
}
