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

// Device loop per IMPLEMENTATION.md §5. The SKAARHOJ routinely cuts mixer
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
		devices[dc.DeviceID] = &device{
			id:           dc.DeviceID,
			ip:           dc.IP,
			dial:         wsDial,
			timing:       defaultTiming,
			store:        newStateStore(),
			connPID:      r.PID("connection"),
			toManager:    toManager,
			fromManager:  make(chan *pb.Parameter, 10),
			wireMessages: defaultWireMessages,
		}
	}
	return devices
}

type device struct {
	id           uint32
	ip           string
	dial         dialFunc
	timing       loopTiming
	store        *stateStore
	connPID      uint32
	toManager    chan<- *pb.Parameter
	fromManager  chan *pb.Parameter           // per-device slice of the manager's output
	wireMessages func(*pb.Parameter) []string // parameter→protocol translation; a seam like dial

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

	// Install the outbound queue before reporting connection=1: once Reactor
	// sees connected, a fromManager write must reach the wire, not the
	// disconnected-discard path.
	out := make(chan string, 64)
	d.mu.Lock()
	d.out = out
	d.mu.Unlock()

	log.Infof("Device %d: connected to mixer at %s (connection=1)", d.id, d.ip)
	d.reportConnection(true)

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

	d.store.clear() // no stale state may survive a power cycle
	d.reportConnection(false)
	log.Warnf("Device %d: mixer %s disconnected (connection=0)", d.id, d.ip)
}

// ingest reduces one inbound line into the state store. Returns true if the
// line carried state. List replies and BMSG are parsed but unused until the
// snapshot milestone.
func (d *device) ingest(line string) bool {
	msg, ok := parseMessage(line)
	if !ok {
		return false
	}
	switch msg.kind {
	case "SETD", "SETS":
		d.store.set(msg.path, msg.value)
		return true
	}
	return false
}

// pumpFromManager drains manager traffic for the life of the device. While
// disconnected, writes are discarded, never queued: replaying stale commands
// at mixer power-on would be surprising and potentially harmful. This stage is
// also where per-device outbound logic will live — e.g. the recording
// in-flight guard of the record milestone (IMPLEMENTATION.md §5) — which is
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
			for _, msg := range d.wireMessages(param) {
				select {
				case out <- msg:
				default:
					log.Errorf("Device %d: outbound queue full, dropping %q", d.id, msg)
				}
			}
		}
	}
}

// defaultWireMessages translates a manager parameter into protocol messages.
// The skeleton registers no controllable parameters, so the real mapping
// arrives with the mute/fader milestone.
func defaultWireMessages(param *pb.Parameter) []string {
	log.Debugf("no wire mapping for parameter %d", param.Id.Parameter)
	return nil
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
