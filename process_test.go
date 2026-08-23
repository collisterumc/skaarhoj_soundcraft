package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/SKAARHOJ/ibeam-corelib-go/ibeam-core"
	b "github.com/SKAARHOJ/ibeam-corelib-go/paramhelpers"
	"github.com/gorilla/websocket"
)

// mockConn scripts one WebSocket session. Inbound frames arrive via frames;
// writes are recorded and never looped back, reproducing the mixer's
// no-self-echo rule (IMPLEMENTATION.md §2.7): the writer never receives the
// SETD/SETS it sent.
type mockConn struct {
	frames chan string

	mu        sync.Mutex
	deadline  time.Time
	writes    []string
	closed    chan struct{}
	closeOnce sync.Once
}

func newMockConn() *mockConn {
	return &mockConn{frames: make(chan string, 16), closed: make(chan struct{})}
}

func (c *mockConn) ReadMessage() (int, []byte, error) {
	c.mu.Lock()
	deadline := c.deadline
	c.mu.Unlock()
	var timeout <-chan time.Time
	if !deadline.IsZero() {
		t := time.NewTimer(time.Until(deadline))
		defer t.Stop()
		timeout = t.C
	}
	select {
	case f := <-c.frames:
		return websocket.TextMessage, []byte(f), nil
	case <-c.closed:
		return 0, nil, errors.New("mock conn closed")
	case <-timeout:
		return 0, nil, errors.New("mock read deadline exceeded")
	}
}

func (c *mockConn) WriteMessage(_ int, data []byte) error {
	select {
	case <-c.closed:
		return errors.New("mock conn closed")
	default:
	}
	c.mu.Lock()
	c.writes = append(c.writes, string(data))
	c.mu.Unlock()
	return nil
}

func (c *mockConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}

func (c *mockConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *mockConn) written() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.writes...)
}

// mockDialer blocks each dial until the test queues an outcome, giving tests
// full control over when the loop reconnects.
type mockDialer struct {
	mu      sync.Mutex
	dials   int
	outcome chan func() (wsConn, error)
}

func newMockDialer() *mockDialer {
	return &mockDialer{outcome: make(chan func() (wsConn, error), 16)}
}

func (m *mockDialer) dial(string) (wsConn, error) {
	m.mu.Lock()
	m.dials++
	m.mu.Unlock()
	f := <-m.outcome
	return f()
}

func (m *mockDialer) dialCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dials
}

func (m *mockDialer) queueConn(c *mockConn) {
	m.outcome <- func() (wsConn, error) { return c, nil }
}

func (m *mockDialer) queueError() {
	m.outcome <- func() (wsConn, error) { return nil, errors.New("mock dial refused") }
}

const testConnPID = 1

// newTestDevice builds and starts a device with test timing; mutate hooks run
// before the loop starts (e.g. to substitute wireMessages).
func newTestDevice(t *testing.T, dial dialFunc, mutate ...func(*device)) (*device, chan *pb.Parameter, chan *pb.Parameter) {
	t.Helper()
	fromManager := make(chan *pb.Parameter, 16)
	toManager := make(chan *pb.Parameter, 100)
	dev := &device{
		id:   1,
		ip:   "203.0.113.1",
		dial: dial,
		timing: loopTiming{
			redialWait:   5 * time.Millisecond,
			alivePeriod:  5 * time.Millisecond,
			readDeadline: 50 * time.Millisecond,
		},
		store:       newStateStore(),
		snapshots:   newSnapshotCache(),
		connPID:     testConnPID,
		toManager:   toManager,
		fromManager: fromManager,
	}
	dev.wireMessages = dev.buildWireMessages
	for _, m := range mutate {
		m(dev)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go dev.run(ctx)
	return dev, fromManager, toManager
}

// awaitConnection consumes toManager until the connection parameter reports the
// wanted state.
func awaitConnection(t *testing.T, toManager <-chan *pb.Parameter, want bool) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case p := <-toManager:
			if p.Id.Parameter == testConnPID && len(p.Value) == 1 && p.Value[0].GetBinary() == want {
				return
			}
		case <-timeout:
			t.Fatalf("timed out waiting for connection=%v", want)
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// withRealWireMapping configures a test device with the real PIDs and mapping
// functions so the loop exercises buildWireMessages and confirmWrite.
func withRealWireMapping(t *testing.T) func(*device) {
	t.Helper()
	r := testRegistry(t)
	return func(d *device) {
		d.inputs = 12 // Ui16
		d.line = 2
		d.pids = mixerPIDs{
			channelMute:     r.PID("channel_mute"),
			channelFader:    r.PID("channel_fader"),
			masterFader:     r.PID("master_fader"),
			snapshotUp:      r.PID("snapshot_up"),
			snapshotDown:    r.PID("snapshot_down"),
			currentSnapshot: r.PID("current_snapshot"),
		}
	}
}

// drainToManager empties toManager until stop is closed, so a full buffer never
// blocks the device loop during a test that does not inspect the confirms.
func drainToManager(toManager <-chan *pb.Parameter, stop <-chan struct{}) {
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-toManager:
			}
		}
	}()
}

// TestFeedbackLoopSafety drives one fader through 100 rapid updates and asserts
// the outbound count stays bounded: exactly one SETD per update, and no extra
// sends triggered by inbound traffic (§2.7 rule 1). The mock never echoes.
func TestFeedbackLoopSafety(t *testing.T) {
	dialer := newMockDialer()
	dev, fromManager, toManager := newTestDevice(t, dialer.dial, withRealWireMapping(t))

	conn := newMockConn()
	dialer.queueConn(conn)
	awaitConnection(t, toManager, true)
	// Drain connection updates so the toManager buffer never blocks the loop.
	stop := make(chan struct{})
	defer close(stop)
	drainToManager(toManager, stop)

	const updates = 100
	for i := 0; i < updates; i++ {
		fromManager <- b.Param(dev.pids.channelFader, dev.id, b.Float(float64(i)/updates, 3))
	}

	waitFor(t, "all fader writes on the wire", func() bool {
		return countSETD(conn.written(), "SETD^i.2.mix^") == updates
	})
	// Give any stray resend a chance to appear, then confirm the count held.
	time.Sleep(30 * time.Millisecond)
	if got := countSETD(conn.written(), "SETD^i.2.mix^"); got != updates {
		t.Errorf("fader SETD count = %d, want exactly %d (no inbound-triggered resends)", got, updates)
	}
	// No other SETD paths were emitted.
	if got := countSETD(conn.written(), "SETD^"); got != updates {
		t.Errorf("total SETD count = %d, want %d", got, updates)
	}
}

// TestOptimisticConfirm asserts that after an outbound write the sent value is
// reported to toManager as current, with no echo from the mock (§2.7).
func TestOptimisticConfirm(t *testing.T) {
	dialer := newMockDialer()
	dev, fromManager, toManager := newTestDevice(t, dialer.dial, withRealWireMapping(t))

	conn := newMockConn()
	dialer.queueConn(conn)
	awaitConnection(t, toManager, true)

	fromManager <- b.Param(dev.pids.channelMute, dev.id, b.Bool(true, 3))

	// The confirm arrives on toManager as the mute parameter, dimension 3, true.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case p := <-toManager:
			if p.Id.Parameter != dev.pids.channelMute {
				continue
			}
			if len(p.Value) != 1 || len(p.Value[0].DimensionID) != 1 || p.Value[0].DimensionID[0] != 3 {
				t.Fatalf("confirm dimension = %v, want [3]", p.Value[0].DimensionID)
			}
			if !p.Value[0].GetBinary() {
				t.Fatalf("confirm value = false, want true")
			}
			// The wire got the write; the mock did not echo it back into the store.
			waitFor(t, "wire write", func() bool {
				return countSETD(conn.written(), "SETD^i.2.mute^1") == 1
			})
			if _, ok := dev.store.get("i.2.mute"); ok {
				t.Error("store has i.2.mute — mock must not echo the write")
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for optimistic confirm")
		}
	}
}

// TestInboundForwardsToManager pushes SETD lines through the session and asserts
// they reach toManager mapped to the right parameter and dimension, and that no
// wire message results (inbound never triggers a send).
func TestInboundForwardsToManager(t *testing.T) {
	dialer := newMockDialer()
	dev, _, toManager := newTestDevice(t, dialer.dial, withRealWireMapping(t))

	conn := newMockConn()
	dialer.queueConn(conn)
	awaitConnection(t, toManager, true)

	conn.frames <- "3:::SETD^i.0.mute^1\nSETD^l.0.mix^0.5\nSETD^m.mix^0.7"

	want := map[uint32][]uint32{
		dev.pids.channelMute:  {1},
		dev.pids.channelFader: {13},
		dev.pids.masterFader:  nil,
	}
	seen := map[uint32]bool{}
	deadline := time.After(2 * time.Second)
	for len(seen) < len(want) {
		select {
		case p := <-toManager:
			dims, ok := want[p.Id.Parameter]
			if !ok {
				continue
			}
			if !dimEqual(p.Value[0].DimensionID, dims) {
				t.Errorf("parameter %d dimension = %v, want %v", p.Id.Parameter, p.Value[0].DimensionID, dims)
			}
			seen[p.Id.Parameter] = true
		case <-deadline:
			t.Fatalf("timed out; saw %v of %v mapped parameters", len(seen), len(want))
		}
	}

	// Inbound traffic must not produce any outbound SETD.
	if got := countSETD(conn.written(), "SETD^"); got != 0 {
		t.Errorf("inbound produced %d outbound SETD frames, want 0", got)
	}
}

// hasFrame reports whether an exact wire frame was written.
func hasFrame(writes []string, msg string) bool {
	for _, w := range writes {
		if w == "3:::"+msg {
			return true
		}
	}
	return false
}

// awaitParameter consumes toManager until the given parameter arrives, then
// returns it. It fails the test on timeout.
func awaitParameter(t *testing.T, toManager <-chan *pb.Parameter, pid uint32) *pb.Parameter {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case p := <-toManager:
			if p.Id.Parameter == pid {
				return p
			}
		case <-deadline:
			t.Fatalf("timed out waiting for parameter %d", pid)
		}
	}
}

// TestSnapshotConnectRequestsList verifies the connect handshake for snapshots:
// SHOWLIST goes out on connect, and once the dump carries var.currentShow the
// loop requests that show's SNAPSHOTLIST.
func TestSnapshotConnectRequestsList(t *testing.T) {
	dialer := newMockDialer()
	_, _, toManager := newTestDevice(t, dialer.dial, withRealWireMapping(t))

	conn := newMockConn()
	dialer.queueConn(conn)
	awaitConnection(t, toManager, true)

	waitFor(t, "SHOWLIST on connect", func() bool {
		return hasFrame(conn.written(), "SHOWLIST")
	})

	// The dump's var.currentShow triggers a SNAPSHOTLIST for that show.
	conn.frames <- "3:::SETS^var.currentShow^ShowA"
	waitFor(t, "SNAPSHOTLIST for current show", func() bool {
		return hasFrame(conn.written(), "SNAPSHOTLIST^ShowA")
	})
}

// TestSnapshotShowlistReplyRequestsList verifies the second trigger path: a
// SHOWLIST reply requests SNAPSHOTLIST for the show already in the store.
func TestSnapshotShowlistReplyRequestsList(t *testing.T) {
	dialer := newMockDialer()
	_, _, toManager := newTestDevice(t, dialer.dial, withRealWireMapping(t))

	conn := newMockConn()
	dialer.queueConn(conn)
	awaitConnection(t, toManager, true)

	// currentShow lands first, then the SHOWLIST reply arrives. The SHOWLIST
	// reply must request the current show's snapshots.
	conn.frames <- "3:::SETS^var.currentShow^ShowA\nSHOWLIST^ShowA^ShowB"
	waitFor(t, "SNAPSHOTLIST from SHOWLIST reply", func() bool {
		return hasFrame(conn.written(), "SNAPSHOTLIST^ShowA")
	})
}

// TestSnapshotStepLoadsAdjacent drives the full path: cache a snapshot list,
// set the current snapshot, then a snapshot_up trigger emits the exact
// LOADSNAPSHOT for the next entry.
func TestSnapshotStepLoadsAdjacent(t *testing.T) {
	dialer := newMockDialer()
	dev, fromManager, toManager := newTestDevice(t, dialer.dial, withRealWireMapping(t))

	conn := newMockConn()
	dialer.queueConn(conn)
	awaitConnection(t, toManager, true)
	stop := make(chan struct{})
	defer close(stop)
	drainToManager(toManager, stop)

	conn.frames <- "3:::SETS^var.currentShow^ShowA\n" +
		"SNAPSHOTLIST^ShowA^Snap1^Snap2^Snap3\n" +
		"SETS^var.currentSnapshot^Snap2"
	waitFor(t, "snapshot list cached", func() bool {
		_, items := dev.snapshots.snapshot()
		return len(items) == 3
	})
	waitFor(t, "current snapshot in store", func() bool {
		v, _ := dev.store.get("var.currentSnapshot")
		return v == "Snap2"
	})

	fromManager <- b.Param(dev.pids.snapshotUp, dev.id, b.Trigger())
	waitFor(t, "LOADSNAPSHOT for next snapshot", func() bool {
		return hasFrame(conn.written(), "LOADSNAPSHOT^ShowA^Snap3")
	})

	fromManager <- b.Param(dev.pids.snapshotDown, dev.id, b.Trigger())
	waitFor(t, "LOADSNAPSHOT for previous snapshot", func() bool {
		return hasFrame(conn.written(), "LOADSNAPSHOT^ShowA^Snap1")
	})
}

// TestCurrentSnapshotFeedback confirms an inbound var.currentSnapshot SETS
// reaches toManager as the current_snapshot string parameter.
func TestCurrentSnapshotFeedback(t *testing.T) {
	dialer := newMockDialer()
	dev, _, toManager := newTestDevice(t, dialer.dial, withRealWireMapping(t))

	conn := newMockConn()
	dialer.queueConn(conn)
	awaitConnection(t, toManager, true)

	conn.frames <- "3:::SETS^var.currentSnapshot^Snap2"
	p := awaitParameter(t, toManager, dev.pids.currentSnapshot)
	if len(p.Value) != 1 || p.Value[0].GetStr() != "Snap2" {
		t.Errorf("current_snapshot = %+v, want Snap2", p.Value)
	}
}

// TestSnapshotCacheClearedOnDisconnect proves the cache does not survive a power
// cycle: after a disconnect and a reconnect without any list replies, a
// snapshot_up trigger is a no-op — no LOADSNAPSHOT reaches the wire.
func TestSnapshotCacheClearedOnDisconnect(t *testing.T) {
	dialer := newMockDialer()
	dev, fromManager, toManager := newTestDevice(t, dialer.dial, withRealWireMapping(t))

	conn1 := newMockConn()
	dialer.queueConn(conn1)
	awaitConnection(t, toManager, true)

	conn1.frames <- "3:::SETS^var.currentShow^ShowA\n" +
		"SNAPSHOTLIST^ShowA^Snap1^Snap2\n" +
		"SETS^var.currentSnapshot^Snap1"
	waitFor(t, "snapshot list cached", func() bool {
		_, items := dev.snapshots.snapshot()
		return len(items) == 2
	})

	conn1.Close()
	awaitConnection(t, toManager, false)
	waitFor(t, "cache cleared on disconnect", func() bool {
		_, items := dev.snapshots.snapshot()
		return len(items) == 0
	})

	// Reconnect, but send no list replies this time.
	conn2 := newMockConn()
	dialer.queueConn(conn2)
	awaitConnection(t, toManager, true)
	waitFor(t, "SHOWLIST re-requested on reconnect", func() bool {
		return hasFrame(conn2.written(), "SHOWLIST")
	})

	// A trigger with an empty cache must not load anything.
	fromManager <- b.Param(dev.pids.snapshotUp, dev.id, b.Trigger())
	time.Sleep(30 * time.Millisecond) // let the pump handle it
	for _, w := range conn2.written() {
		if strings.HasPrefix(w, "3:::LOADSNAPSHOT") {
			t.Errorf("empty cache produced a LOADSNAPSHOT: %q", w)
		}
	}
}

// TestSnapshotShowChangeRerequests confirms a var.currentShow change from the
// mixer triggers a fresh SNAPSHOTLIST for the new show.
func TestSnapshotShowChangeRerequests(t *testing.T) {
	dialer := newMockDialer()
	_, _, toManager := newTestDevice(t, dialer.dial, withRealWireMapping(t))

	conn := newMockConn()
	dialer.queueConn(conn)
	awaitConnection(t, toManager, true)

	conn.frames <- "3:::SETS^var.currentShow^ShowA"
	waitFor(t, "SNAPSHOTLIST for first show", func() bool {
		return hasFrame(conn.written(), "SNAPSHOTLIST^ShowA")
	})

	conn.frames <- "3:::SETS^var.currentShow^ShowB"
	waitFor(t, "SNAPSHOTLIST for changed show", func() bool {
		return hasFrame(conn.written(), "SNAPSHOTLIST^ShowB")
	})
}

func countSETD(writes []string, prefix string) int {
	n := 0
	for _, w := range writes {
		if strings.HasPrefix(w, "3:::"+prefix) {
			n++
		}
	}
	return n
}

func TestReconnectStateMachine(t *testing.T) {
	dialer := newMockDialer()
	_, _, toManager := newTestDevice(t, dialer.dial)

	awaitConnection(t, toManager, false) // initial state

	// Two refused dials, then a working connection.
	dialer.queueError()
	dialer.queueError()
	conn1 := newMockConn()
	dialer.queueConn(conn1)
	awaitConnection(t, toManager, true)
	if got := dialer.dialCount(); got != 3 {
		t.Errorf("dial count = %d, want 3", got)
	}

	// Remote close (mixer sent a FIN) → disconnect, then redial succeeds.
	conn1.Close()
	awaitConnection(t, toManager, false)
	conn2 := newMockConn()
	dialer.queueConn(conn2)
	awaitConnection(t, toManager, true)
	if got := dialer.dialCount(); got != 4 {
		t.Errorf("dial count after reconnect = %d, want 4", got)
	}
}

func TestDeadLinkDetection(t *testing.T) {
	dialer := newMockDialer()
	_, _, toManager := newTestDevice(t, dialer.dial)

	// conn1 delivers one frame, then goes silent without closing (power cut, no FIN).
	conn1 := newMockConn()
	conn1.frames <- "3:::SETD^i.0.mute^1"
	dialer.queueConn(conn1)
	awaitConnection(t, toManager, true)

	// The read deadline must kill the session and trigger a redial.
	awaitConnection(t, toManager, false)
	conn2 := newMockConn()
	dialer.queueConn(conn2)
	awaitConnection(t, toManager, true)
	if got := dialer.dialCount(); got != 2 {
		t.Errorf("dial count = %d, want 2", got)
	}
}

// TestAliveKeepalive verifies periodic ALIVE frames reach the wire. It does
// not prove writer exclusivity — the mock serializes writes, so a second
// writer goroutine would go undetected here.
func TestAliveKeepalive(t *testing.T) {
	dialer := newMockDialer()
	_, _, toManager := newTestDevice(t, dialer.dial)

	conn := newMockConn()
	dialer.queueConn(conn)
	awaitConnection(t, toManager, true)

	waitFor(t, "two ALIVE frames", func() bool {
		alive := 0
		for _, w := range conn.written() {
			if w == "3:::ALIVE" {
				alive++
			}
		}
		return alive >= 2
	})
}

func TestStoreClearedAndWritesDiscardedWhileDisconnected(t *testing.T) {
	dialer := newMockDialer()
	dev, fromManager, toManager := newTestDevice(t, dialer.dial, func(d *device) {
		d.wireMessages = func(*pb.Parameter) []string { return []string{"SETD^i.0.mute^1"} }
	})

	conn1 := newMockConn()
	dialer.queueConn(conn1)
	awaitConnection(t, toManager, true)

	// State ingest: SETD/SETS land in the store, non-data frames are ignored.
	conn1.frames <- "3:::SETD^i.0.mute^1\nSETS^var.currentShow^Training"
	conn1.frames <- "2::"
	waitFor(t, "state in store", func() bool { return dev.store.size() == 2 })
	if v, _ := dev.store.get("var.currentShow"); v != "Training" {
		t.Errorf("store var.currentShow = %q, want Training", v)
	}

	// Disconnect clears the store.
	conn1.Close()
	awaitConnection(t, toManager, false)
	if got := dev.store.size(); got != 0 {
		t.Errorf("store size after disconnect = %d, want 0", got)
	}

	// A write while disconnected is discarded, not queued for the next session.
	fromManager <- b.Param(2, 1, b.Bool(true))
	waitFor(t, "pump drains fromManager", func() bool { return len(fromManager) == 0 })
	time.Sleep(20 * time.Millisecond) // let the pump finish handling the parameter

	conn2 := newMockConn()
	dialer.queueConn(conn2)
	awaitConnection(t, toManager, true)

	// While connected the same write goes out on the wire.
	fromManager <- b.Param(2, 1, b.Bool(true))
	waitFor(t, "wire message sent", func() bool {
		for _, w := range conn2.written() {
			if w == "3:::SETD^i.0.mute^1" {
				return true
			}
		}
		return false
	})

	// Exactly one SETD frame: the disconnected-phase write must not have been replayed.
	setds := 0
	for _, w := range conn2.written() {
		if strings.HasPrefix(w, "3:::SETD") {
			setds++
		}
	}
	if setds != 1 {
		t.Errorf("SETD frames on reconnect = %d, want 1 (disconnected write must be discarded)", setds)
	}

	// No self-echo from the mock: our own write never came back as inbound state.
	if _, ok := dev.store.get("i.0.mute"); ok {
		t.Error("store contains i.0.mute — mock echoed a write back")
	}
}
