package testtracer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// M-16. FR-08 AC1
func TestJSONEmitter_WritesOneLinePerPacket(t *testing.T) {
	out := &syncBuffer{}
	em := newJSONEmitter(out)

	require.NoError(t, em.Emit(samplePacket(1)))
	require.NoError(t, em.Emit(samplePacket(2)))

	raw := out.String()
	assert.True(t, strings.HasSuffix(raw, "\n"), "each packet must be newline-terminated")
	lines := out.Lines()
	require.Len(t, lines, 2)
	for _, l := range lines {
		assert.NotContains(t, l, "\n")
		assert.True(t, json.Valid([]byte(l)), "line is not valid JSON: %s", l)
	}
}

// M-16. FR-08 AC2 / spec §5: exact key set and embedded JSON values.
func TestJSONEmitter_PacketSchema(t *testing.T) {
	out := &syncBuffer{}
	require.NoError(t, newJSONEmitter(out).Emit(samplePacket(1)))
	lines := out.Lines()
	require.Len(t, lines, 1)

	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &obj))

	wantKeys := []string{
		"module", "partner_id", "rule", "packet_index", "auction_id", "started_at", "completed_at",
		"incoming_request", "bidder_requests", "bidder_responses", "final_response",
	}
	for _, k := range wantKeys {
		assert.Contains(t, obj, k)
	}
	assert.Len(t, obj, len(wantKeys), "unexpected extra top-level keys")

	assert.JSONEq(t, `"test_provider.test_tracer"`, string(obj["module"]))
	assert.JSONEq(t, `{"partner_id":"664-025-677-881","duration":"10m0s","trace_packets_amount":3}`, string(obj["rule"]))

	var incoming struct {
		Timestamp string          `json:"timestamp"`
		Body      json.RawMessage `json:"body"`
	}
	require.NoError(t, json.Unmarshal(obj["incoming_request"], &incoming))
	assert.True(t, strings.HasPrefix(string(incoming.Body), "{"), "body must be an embedded JSON object, not a string")

	var bidderReqs []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(obj["bidder_requests"], &bidderReqs))
	require.Len(t, bidderReqs, 1)
	assert.JSONEq(t, `"appnexus"`, string(bidderReqs[0]["bidder"]))
	assert.True(t, strings.HasPrefix(string(bidderReqs[0]["request"]), "{"))

	var bidderResps []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(obj["bidder_responses"], &bidderResps))
	require.Len(t, bidderResps, 1)
	assert.JSONEq(t, `{"currency":"USD","bids":[]}`, string(bidderResps[0]["response"]))
}

// M-19. FR-08 AC5
func TestJSONEmitter_TimestampsAreRFC3339NanoUTC(t *testing.T) {
	out := &syncBuffer{}
	p := samplePacket(1)
	p.StartedAt = time.Date(2026, 9, 16, 12, 30, 45, 123456789, time.FixedZone("EEST", 3*3600))
	require.NoError(t, newJSONEmitter(out).Emit(p))
	lines := out.Lines()
	require.Len(t, lines, 1)

	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &obj))

	var started string
	require.NoError(t, json.Unmarshal(obj["started_at"], &started))
	assert.Equal(t, "2026-09-16T09:30:45.123456789Z", started, "timestamps must be normalised to UTC")
	_, err := time.Parse(time.RFC3339Nano, started)
	assert.NoError(t, err)
}

// M-28. design §8: write failures are reported to the caller (the hook logs and continues).
// FR-15 AC2: a stdout write failure is returned to the hook, which logs it.
func TestJSONEmitter_WriteErrorIsReturned(t *testing.T) {
	boom := errors.New("stdout closed")
	err := newJSONEmitter(failingWriter{err: boom}).Emit(samplePacket(1))
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

// M-35. FR-08 AC1 / NFR-01: the asynchronous emitter delivers packets in order without blocking the
// caller, and Close drains the queue.
func TestAsyncEmitter_DeliversInOrderAndDrainsOnClose(t *testing.T) {
	out := &syncBuffer{}
	em := newAsyncEmitter(newJSONEmitter(out), 8)

	for i := 1; i <= 5; i++ {
		require.NoError(t, em.Emit(samplePacket(i)))
	}
	require.NoError(t, em.Close())
	require.NoError(t, em.Close(), "Close is idempotent")

	packets := out.Packets(t)
	require.Len(t, packets, 5)
	for i, p := range packets {
		assert.Equal(t, i+1, p.PacketIndex)
	}
	assert.ErrorIs(t, em.Emit(samplePacket(6)), ErrEmitterClosed)
	assert.Zero(t, em.Dropped())
}

// M-35. NFR-01: with stdout stalled the queue fills up; further packets are dropped and counted, and
// Emit still returns at once. The queued packets are written once stdout resumes.
func TestAsyncEmitter_DropsWhenQueueIsFullAndStdoutStalls(t *testing.T) {
	w := &blockingWriter{entered: make(chan struct{}), release: make(chan struct{})}
	const queue = 4
	em := newAsyncEmitter(newJSONEmitter(w), queue)

	require.NoError(t, em.Emit(samplePacket(0))) // taken by the writer, blocked in Write
	<-w.entered
	for i := 1; i <= queue; i++ {
		require.NoError(t, em.Emit(samplePacket(i)), "packet %d fits in the queue", i)
	}

	done := make(chan error, 1)
	go func() { done <- em.Emit(samplePacket(queue + 1)) }()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, ErrQueueFull)
	case <-time.After(2 * time.Second):
		t.Fatal("Emit blocked on a full queue")
	}
	assert.EqualValues(t, 1, em.Dropped())

	close(w.release)
	require.NoError(t, em.Close())
	assert.EqualValues(t, queue+1, w.writes.Load(), "the blocked packet and the queued ones are written")
}
