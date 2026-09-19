package testtracer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Emitter writes trace packets. The production implementation writes NDJSON to stdout.
type Emitter interface {
	Emit(packet TracePacket) error
}

// ErrEmitterClosed is returned by Emit after Close.
var ErrEmitterClosed = errors.New("emitter is closed")

// ErrQueueFull is returned by asyncEmitter.Emit when the packet had to be dropped.
var ErrQueueFull = errors.New("trace queue is full, packet dropped")

// defaultQueueSize bounds the packets waiting for stdout. Each packet holds the full auction (tens of
// KB with debug responses), so the queue is kept small; the rules already cap how many exist.
const defaultQueueSize = 64

// asyncEmitter decouples the hooks from stdout. Emit enqueues and returns at once; one goroutine drains
// the queue into next. A stalled stdout therefore never delays an auction response (NFR-01): the
// queue fills up and further packets are dropped, counted and logged, instead of blocking exitpoint.
// Close, called from the module's Shutdown, waits for the queue to drain.
type asyncEmitter struct {
	next    Emitter
	queue   chan TracePacket
	done    chan struct{}
	dropped atomic.Int64

	// mu orders Emit against Close: Emit holds it shared while it sends, Close holds it exclusively
	// while it closes the channel, so a hook still running during shutdown can never send on a
	// closed channel. Uncontended in normal operation.
	mu     sync.RWMutex
	closed bool
}

func newAsyncEmitter(next Emitter, queueSize int) *asyncEmitter {
	e := &asyncEmitter{next: next, queue: make(chan TracePacket, queueSize), done: make(chan struct{})}
	go e.drain()
	return e
}

func (e *asyncEmitter) drain() {
	defer close(e.done)
	for p := range e.queue {
		if err := e.next.Emit(p); err != nil {
			warnf("auction %s: %v", p.AuctionID, err)
		}
	}
}

// Emit enqueues the packet without blocking. It returns ErrQueueFull when the queue is full and the
// packet was dropped, or ErrEmitterClosed after Close.
func (e *asyncEmitter) Emit(packet TracePacket) error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return ErrEmitterClosed
	}
	select {
	case e.queue <- packet:
		return nil
	default:
		return fmt.Errorf("%w (%d dropped so far)", ErrQueueFull, e.dropped.Add(1))
	}
}

// Dropped returns the number of packets dropped because the queue was full.
func (e *asyncEmitter) Dropped() int64 { return e.dropped.Load() }

// Close stops accepting packets and waits until the queued ones are written. Idempotent.
func (e *asyncEmitter) Close() error {
	e.mu.Lock()
	if !e.closed {
		e.closed = true
		close(e.queue)
	}
	e.mu.Unlock()
	<-e.done
	return nil
}

// jsonEmitter writes one JSON object per line to w with a single Write call per packet, so
// concurrent emits from different requests never interleave (FR-08, FR-16).
type jsonEmitter struct {
	mu sync.Mutex
	w  io.Writer
}

func newJSONEmitter(w io.Writer) *jsonEmitter {
	return &jsonEmitter{w: w}
}

// Emit marshals packet, appends '\n' and writes it atomically with respect to other Emit calls.
// All timestamps are normalised to UTC (FR-08 AC5).
func (e *jsonEmitter) Emit(packet TracePacket) error {
	raw, err := json.Marshal(packet.utc())
	if err != nil {
		return fmt.Errorf("%s: marshal trace packet: %w", ModuleCode, err)
	}
	raw = append(raw, '\n')

	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := e.w.Write(raw); err != nil {
		return fmt.Errorf("%s: write trace packet: %w", ModuleCode, err)
	}
	return nil
}
