// services/telemetry/internal/hub/hub.go

// Package hub implements the SSE fan-out used by every telemetry stream:
// per-connection bounded buffers with drop-oldest backpressure, an explicit
// dropped-count marker event (never silent loss), sticky events replayed to
// new subscribers, and heartbeat comments to keep intermediaries alive.
package hub

import (
	"bufio"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Event is one server-sent event. Tag is out-of-band metadata used for
// subscription filtering (log tab filtering); it is not serialized itself.
type Event struct {
	Name string
	Data []byte
	Tag  string
}

// Conn is one subscriber connection with a bounded FIFO buffer.
type Conn struct {
	mu      sync.Mutex
	buf     []Event
	max     int
	dropped uint64
	notify  chan struct{}
	filter  func(Event) bool
}

func (c *Conn) push(ev Event) {
	if c.filter != nil && !c.filter(ev) {
		return
	}
	c.mu.Lock()
	if len(c.buf) >= c.max {
		copy(c.buf, c.buf[1:])
		c.buf = c.buf[:len(c.buf)-1]
		c.dropped++
	}
	c.buf = append(c.buf, ev)
	c.mu.Unlock()
	select {
	case c.notify <- struct{}{}:
	default:
	}
}

// Drain returns and clears the pending events plus the count of events
// dropped since the previous drain.
func (c *Conn) Drain() ([]Event, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	evs := c.buf
	c.buf = make([]Event, 0, c.max)
	d := c.dropped
	c.dropped = 0
	return evs, d
}

// Hub fans events out to subscribers.
type Hub struct {
	mu      sync.Mutex
	conns   map[*Conn]struct{}
	sticky  map[string]Event
	bufSize int
}

// New creates a Hub whose subscriber buffers hold bufSize events.
func New(bufSize int) *Hub {
	if bufSize < 1 {
		bufSize = 1
	}
	return &Hub{
		conns:   make(map[*Conn]struct{}),
		sticky:  make(map[string]Event),
		bufSize: bufSize,
	}
}

// Publish delivers ev to every subscriber, dropping each subscriber's oldest
// buffered event on overflow.
func (h *Hub) Publish(ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.conns {
		c.push(ev)
	}
}

// PublishSticky publishes ev and remembers it by event name; new subscribers
// receive the latest sticky event of each name on subscribe.
func (h *Hub) PublishSticky(ev Event) {
	h.mu.Lock()
	h.sticky[ev.Name] = ev
	for c := range h.conns {
		c.push(ev)
	}
	h.mu.Unlock()
}

// Subscribe registers a new connection. filter may be nil; when set, only
// events it accepts are buffered.
func (h *Hub) Subscribe(filter func(Event) bool) *Conn {
	c := &Conn{
		buf:    make([]Event, 0, h.bufSize),
		max:    h.bufSize,
		notify: make(chan struct{}, 1),
		filter: filter,
	}
	h.mu.Lock()
	for _, ev := range h.sticky {
		c.push(ev)
	}
	h.conns[c] = struct{}{}
	h.mu.Unlock()
	return c
}

// Unsubscribe removes the connection from the hub.
func (h *Hub) Unsubscribe(c *Conn) {
	h.mu.Lock()
	delete(h.conns, c)
	h.mu.Unlock()
}

// ServeSSE streams the subscription over w until the request context ends.
// It writes SSE headers, replays sticky events, emits heartbeat comments
// every heartbeat interval, and precedes any post-drop delivery with an
// explicit "dropped" marker event carrying the drop count.
func (h *Hub) ServeSSE(w http.ResponseWriter, r *http.Request, filter func(Event) bool, heartbeat time.Duration) {
	conn := h.Subscribe(filter)
	defer h.Unsubscribe(conn)
	h.ServeConn(w, r, conn, heartbeat)
}

// ServeConn streams an existing subscription over w until the request
// context ends. Callers normally use ServeSSE; ServeConn exists so the
// framing path can be driven against a pre-loaded connection.
func (h *Hub) ServeConn(w http.ResponseWriter, r *http.Request, conn *Conn, heartbeat time.Duration) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming unsupported"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	bw := bufio.NewWriter(w)
	fmt.Fprint(bw, ": connected\n\n")
	bw.Flush()
	flusher.Flush()

	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fmt.Fprint(bw, ": hb\n\n")
			if bw.Flush() != nil {
				return
			}
			flusher.Flush()
		case <-conn.notify:
			// Drain until the buffer is confirmed empty before blocking again.
			// A push that lands between a Drain and the next select can find
			// the notify slot already full; without this re-check its event
			// would wait for the next push or heartbeat to be flushed.
			for {
				evs, dropped := conn.Drain()
				if dropped == 0 && len(evs) == 0 {
					break
				}
				if dropped > 0 {
					fmt.Fprintf(bw, "event: dropped\ndata: {\"dropped\":%d}\n\n", dropped)
				}
				for _, ev := range evs {
					fmt.Fprintf(bw, "event: %s\ndata: %s\n\n", ev.Name, ev.Data)
				}
				if bw.Flush() != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}
