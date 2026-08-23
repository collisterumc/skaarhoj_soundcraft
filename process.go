package main

import (
	"context"
	"sync"
	"time"

	ib "github.com/SKAARHOJ/ibeam-corelib-go"
	pb "github.com/SKAARHOJ/ibeam-corelib-go/ibeam-core"
	b "github.com/SKAARHOJ/ibeam-corelib-go/paramhelpers"
	"github.com/gorilla/websocket"
	log "github.com/s00500/env_logger"
)

// Device loop. The SKAARHOJ routinely cuts mixer
// power, so disconnects are normal operation: reconnect forever with 2 s
// backoff, detect dead links with a read deadline (a power cut sends no FIN),
// and log connection transitions once, not per retry.
type loopTiming struct {
	redialWait   time.Duration
	alivePeriod  time.Duration
	readDeadline time.Duration
}

var defaultTiming = loopTiming{
	redialWait:   2 * time.Second,
	alivePeriod:  time.Second, // mandatory: the mixer drops silent clients after ~19 s
	readDeadline: 5 * time.Second,
}

// wsConn is the subset of *websocket.Conn the device loop uses; tests
// substitute mocks.
type wsConn interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	SetReadDeadline(t time.Time) error
	Close() error
}

type dialFunc func(url string) (wsConn, error)

func wsDial(url string) (wsConn, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// processDevices owns the manager-facing side of all configured mixers: it
// brings up one connection loop per active device, then spends its life
// fanning fromManager traffic out to the loop that owns each parameter.
func processDevices(r *ib.IBeamParameterRegistry, config CoreConfig, fromManager <-chan *pb.Parameter, toManager chan<- *pb.Parameter) {
	devices := buildDevices(r, config, toManager)
	for _, dev := range devices {
		go dev.run(context.Background())
	}

	for param := range fromManager {
		dev, known := devices[param.Id.Device]
		if !known {
			log.Errorf("Device %d: dropping parameter %d for unknown or inactive device", param.Id.Device, param.Id.Parameter)
			continue
		}
		dev.queue(param)
	}
}

// buildDevices registers every active configured mixer with the corelib and
// constructs its device loop, keyed by device ID. All registrations finish
// before any loop starts, so Reactor sees the complete device list at once.
func buildDevices(r *ib.IBeamParameterRegistry, config CoreConfig, toManager chan<- *pb.Parameter) map[uint32]*device {
	devices := make(map[uint32]*device)
	for _, dc := range config.Devices {
		if !dc.Active {
			continue
		}
		if _, err := r.RegisterDevice(dc.DeviceID, dc.ModelID); log.Should(err) {
			continue
		}
		ch := modelChannels(dc.ModelID)
		dev := &device{
			id:          dc.DeviceID,
			ip:          dc.IP,
			dial:        wsDial,
			timing:      defaultTiming,
			store:       newStateStore(),
			snapshots:   newSnapshotCache(),
			connPID:     r.PID("connection"),
			toManager:   toManager,
			fromManager: make(chan *pb.Parameter, 10),
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
				recordMultitrack: r.PIDByModel("record_multitrack", dc.ModelID),
				multitrackBusy:   r.PIDByModel("multitrack_busy", dc.ModelID),
				multitrackTime:   r.PIDByModel("multitrack_time", dc.ModelID),
			},
			record2trackGuard:     newRecordGuard(recordGuardTimeout),
			recordMultitrackGuard: newRecordGuard(recordGuardTimeout),
			inputs:                ch.inputs,
			line:                  ch.line,
		}
		dev.wireMessages = dev.buildWireMessages
		devices[dc.DeviceID] = dev
	}
	return devices
}

// mixerPIDs holds the corelib parameter IDs the device loop maps to and from
// the wire. Resolved once at registration so the hot path never looks them up.
type mixerPIDs struct {
	channelMute     uint32
	channelFader    uint32
	channelFaderDB  uint32
	masterFader     uint32
	masterFaderDB   uint32
	snapshotUp      uint32
	snapshotDown    uint32
	currentSnapshot uint32
	record2track    uint32
	recordBusy      uint32
	// Multitrack PIDs are 0 on non-Ui24R models (parameter not registered).
	recordMultitrack uint32
	multitrackBusy   uint32
	multitrackTime   uint32
}

// recordGuardTimeout backstops the in-flight guard if the mixer never reports
// the recording state (e.g. no USB stick). Comfortably past the measured ~206 ms
// command-to-state latency.
const recordGuardTimeout = 2 * time.Second

type device struct {
	id        uint32
	ip        string
	dial      dialFunc
	timing    loopTiming
	store     *stateStore
	snapshots *snapshotCache
	connPID   uint32
	pids      mixerPIDs
	// In-flight guards, one per recording toggle, absorb corelib retry
	// double-fires until the mixer confirms the new state (see recordGuard).
	record2trackGuard     *recordGuard
	recordMultitrackGuard *recordGuard
	inputs                int // input channels; the channel dimension splits i.<n> from l.<n> here
	line                  int // line-in channels; bounds the l.<n> range on inbound paths
	toManager             chan<- *pb.Parameter
	fromManager           chan *pb.Parameter           // per-device slice of the manager's output
	wireMessages          func(*pb.Parameter) []string // parameter→protocol translation; a seam like dial

	mu  sync.Mutex
	out chan<- string // session outbound queue; nil while disconnected
}

// queue hands a manager parameter to this device's loop without ever blocking
// the shared fan-out; if the loop has fallen behind, the parameter is dropped.
func (d *device) queue(param *pb.Parameter) {
	select {
	case d.fromManager <- param:
	default:
		log.Errorf("Device %d: queue overrun, dropping parameter %d", d.id, param.Id.Parameter)
	}
}

// send puts a raw wire message on the session outbound queue. Both the reader
// goroutine (list-reply follow-ups) and the pump goroutine (writes) call it, so
// it takes the outbound channel under the same lock the session installs and
// clears. Returns false when disconnected or the queue is full.
func (d *device) send(msg string) bool {
	d.mu.Lock()
	out := d.out
	d.mu.Unlock()
	if out == nil {
		return false
	}
	select {
	case out <- msg:
		return true
	default:
		log.Errorf("Device %d: outbound queue full, dropping %q", d.id, msg)
		return false
	}
}

// run reconnects forever; it only returns when ctx is cancelled (tests).
func (d *device) run(ctx context.Context) {
	d.reportConnection(false)
	go d.pumpFromManager(ctx)

	announcedDown := false // log transitions once, not every retry
	for {
		conn, err := d.dial("ws://" + d.ip)
		if err != nil {
			if !announcedDown {
				log.Warnf("Device %d: mixer %s unreachable, retrying every %s: %v", d.id, d.ip, d.timing.redialWait, err)
				announcedDown = true
			}
			if !sleepCtx(ctx, d.timing.redialWait) {
				return
			}
			continue
		}

		d.session(ctx, conn)
		announcedDown = true // session logged the disconnect
		if ctx.Err() != nil {
			return
		}
		if !sleepCtx(ctx, d.timing.redialWait) {
			return
		}
	}
}

// session runs one connection until it dies (read error, deadline, or ctx cancel).
func (d *device) session(ctx context.Context, conn wsConn) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Clear the record guards at session start as well as on disconnect. The
	// disconnect clear can race a pumpFromManager iteration that arms a guard just
	// after cleanup; clearing here guarantees each session begins clean.
	if d.record2trackGuard != nil {
		d.record2trackGuard.clear()
	}
	if d.recordMultitrackGuard != nil {
		d.recordMultitrackGuard.clear()
	}

	// Install the outbound queue before reporting connection=1: once Reactor
	// sees connected, a fromManager write must reach the wire, not the
	// disconnected-discard path.
	out := make(chan string, 64)
	d.mu.Lock()
	d.out = out
	d.mu.Unlock()

	log.Infof("Device %d: connected to mixer at %s (connection=1)", d.id, d.ip)
	d.reportConnection(true)

	// Request the show list so the snapshot cache can be rebuilt from the current
	// show's SNAPSHOTLIST. The mixer never replies to an unknown command, so a
	// silent SHOWLIST simply leaves the cache empty.
	d.send("SHOWLIST")

	// Single writer goroutine: websocket conns are not concurrent-write-safe.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		alive := time.NewTicker(d.timing.alivePeriod)
		defer alive.Stop()
		for {
			var msg string
			select {
			case <-sctx.Done():
				return
			case msg = <-out:
			case <-alive.C:
				msg = "ALIVE"
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(encodeFrame(msg))); err != nil {
				cancel()
				return
			}
		}
	}()

	gotState := false
	for sctx.Err() == nil {
		if err := conn.SetReadDeadline(time.Now().Add(d.timing.readDeadline)); err != nil {
			break
		}
		// The mixer chatters continuously (worst observed gap 2.65 s), so a
		// read-deadline hit means the link is dead — drop and redial.
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		lines, ok := decodeFrame(string(data))
		if !ok {
			continue
		}
		for _, line := range lines {
			if d.ingest(line) && !gotState {
				gotState = true
				log.Infof("Device %d: receiving mixer state (first message: %s)", d.id, line)
			}
		}
	}

	cancel()
	d.mu.Lock()
	d.out = nil
	d.mu.Unlock()
	conn.Close()
	wg.Wait()

	d.store.clear()     // no stale state may survive a power cycle
	d.snapshots.clear() // the cache must not survive a power cycle either
	if d.record2trackGuard != nil {
		d.record2trackGuard.clear()
	}
	if d.recordMultitrackGuard != nil {
		d.recordMultitrackGuard.clear()
	}
	d.reportConnection(false)
	log.Warnf("Device %d: mixer %s disconnected (connection=0)", d.id, d.ip)
}

// ingest reduces one inbound line into the state store. Returns true if the
// line carried state. It also drives the snapshot cache: a SHOWLIST reply or a
// var.currentShow change requests the current show's SNAPSHOTLIST, and a
// SNAPSHOTLIST reply fills the cache.
func (d *device) ingest(line string) bool {
	msg, ok := parseMessage(line)
	if !ok {
		return false
	}
	switch msg.kind {
	case "SETD", "SETS":
		if msg.kind == "SETS" && msg.path == "var.currentShow" {
			d.requestSnapshotList(msg.value)
		}
		d.store.set(msg.path, msg.value)
		// Map recognized paths to current values. Inbound traffic never triggers
		// a wire send — only fromManager writes go out.
		for _, p := range d.inboundParameter(msg.path, msg.value) {
			d.toManager <- p
		}
		return true
	case "SHOWLIST":
		// SHOWLIST does not name the current show; read it from the store (the
		// dump carries var.currentShow) and request its snapshots.
		if show, ok := d.store.get("var.currentShow"); ok {
			d.requestSnapshotList(show)
		}
	case "SNAPSHOTLIST":
		if reply, ok := parseListReply(msg, true); ok {
			d.snapshots.setList(reply.key, reply.items)
		}
	}
	return false
}

// requestSnapshotList points the cache at show and asks the mixer for its
// snapshots. A silent reply (unknown command) leaves the cache empty. Two
// events request a list: the SHOWLIST reply and a var.currentShow change. Both
// can fire near connect for the same show; the cache makes a repeat request
// harmless.
func (d *device) requestSnapshotList(show string) {
	d.snapshots.setShow(show)
	d.send("SNAPSHOTLIST^" + show)
}

// pumpFromManager drains manager traffic for the life of the device. While
// disconnected, writes are discarded, never queued: replaying stale commands
// at mixer power-on would be surprising and potentially harmful. This stage is
// also where per-device outbound logic will live — e.g. the recording
// in-flight guard of the record milestone — which is
// why it exists separately rather than being folded into the fan-out.
func (d *device) pumpFromManager(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case param := <-d.fromManager:
			d.mu.Lock()
			out := d.out
			d.mu.Unlock()
			if out == nil {
				log.Debugf("Device %d: discarding write for parameter %d while disconnected", d.id, param.Id.Parameter)
				continue
			}
			msgs := d.wireMessages(param)
			for _, msg := range msgs {
				select {
				case out <- msg:
				default:
					log.Errorf("Device %d: outbound queue full, dropping %q", d.id, msg)
				}
			}
			// Optimistic confirm: the mixer never echoes a write, so feed the
			// sent value back as current now. Confirm
			// only after a wire message actually went out, so an unmapped
			// parameter confirms nothing.
			if len(msgs) > 0 {
				for _, confirm := range d.confirmWrite(param) {
					d.toManager <- confirm
				}
			}
		}
	}
}

func (d *device) reportConnection(connected bool) {
	d.toManager <- b.Param(d.connPID, d.id, b.Bool(connected))
}

// sleepCtx sleeps for wait; returns false if ctx was cancelled first.
func sleepCtx(ctx context.Context, wait time.Duration) bool {
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
