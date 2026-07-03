package server

// The send queue's contract: a client whose socket never drains stalls only
// its own writer goroutine — the instance tick keeps its rate — and once
// its queue fills, the client is closed and removed instead of shedding
// backpressure onto everyone else's frames.

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
)

// stalledTransport never completes a write until closed, like a peer whose
// receive window went to zero and stayed there.
type stalledTransport struct {
	closeOnce sync.Once
	closed    chan struct{}
}

func newStalledTransport() *stalledTransport {
	return &stalledTransport{closed: make(chan struct{})}
}

func (s *stalledTransport) ReadFrame() ([]byte, error) {
	<-s.closed
	return nil, io.EOF
}

func (s *stalledTransport) WriteFrame(frame []byte, binary bool) error {
	<-s.closed
	return errors.New("stalled transport closed")
}

func (s *stalledTransport) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

// TestStalledClientDoesNotStallTick: with per-client send queues, a wedged
// socket costs its owner the connection and costs the instance nothing.
// (Before the queues, every frame to it stalled the tick loop for up to
// writeTimeout — 1s — apiece.)
func TestStalledClientDoesNotStallTick(t *testing.T) {
	lb, err := NewLobby(content.DB(), Config{
		Addr: "127.0.0.1:0", Seed: 7, TickInterval: 2 * time.Millisecond,
		Map: &protocol.MapSpec{Width: 16, Height: 16, Rooms: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go lb.ListenAndServe(ctx)
	if lb.Addr() == nil {
		t.Fatal("lobby failed to listen")
	}

	lb.mu.Lock()
	in, err := lb.newInstanceLocked()
	lb.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	tr := newStalledTransport()
	c := newClient(tr, modeJSONWorld)
	c.inst = in
	in.mu.Lock()
	in.joins = append(in.joins, c)
	in.mu.Unlock()
	go readLoop(ctx, c)

	tick := func() uint64 {
		v, err := in.runOnTick(func() (any, error) { return in.sim.W.Tick, nil })
		if err != nil {
			t.Fatal(err)
		}
		return v.(uint64)
	}

	deadline := time.Now().Add(5 * time.Second)
	for in.clientN.Load() == 0 { // the join lands on a tick
		if time.Now().After(deadline) {
			t.Fatal("stalled client never joined")
		}
		time.Sleep(2 * time.Millisecond)
	}
	// The queue (64 frames) fills within ~200 sends; the client must then
	// be closed and removed while the tick rate never dips. 2s of real time
	// at a 2ms tick is ~1000 ticks; the old synchronous path would have
	// managed about two.
	for in.clientN.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("stalled client was never disconnected")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-tr.closed:
	default:
		t.Error("stalled client removed but its transport never closed")
	}
	// The queue fills after ~65 sends ≈ 195 ticks, so reaching removal at
	// all proves the loop kept its rate — the pre-queue behavior would have
	// managed ~5 frames (one per writeTimeout) before the deadline. The
	// tick floor is deliberately loose; CI once landed exactly on a tight
	// one.
	if got := tick(); got < 100 {
		t.Errorf("tick %d after the stall window; the queue is not shielding the loop", got)
	}
}
