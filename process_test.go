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
		store:        newStateStore(),
		connPID:      testConnPID,
		toManager:    toManager,
		fromManager:  fromManager,
		wireMessages: defaultWireMessages,
	}
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
