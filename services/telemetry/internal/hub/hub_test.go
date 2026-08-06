// services/telemetry/internal/hub/hub_test.go

package hub

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDropOldestWithMarker(t *testing.T) {
	h := New(4)
	c := h.Subscribe(nil)
	for i := 0; i < 10; i++ {
		h.Publish(Event{Name: "e", Data: []byte(fmt.Sprintf(`{"i":%d}`, i))})
	}
	evs, dropped := c.Drain()
	if dropped != 6 {
		t.Fatalf("dropped = %d, want 6", dropped)
	}
	if len(evs) != 4 {
		t.Fatalf("len(evs) = %d, want 4", len(evs))
	}
	for i, ev := range evs {
		want := fmt.Sprintf(`{"i":%d}`, i+6)
		if string(ev.Data) != want {
			t.Fatalf("evs[%d].Data = %s, want %s", i, ev.Data, want)
		}
	}
	if evs2, d2 := c.Drain(); len(evs2) != 0 || d2 != 0 {
		t.Fatalf("second drain not empty: %d events, %d dropped", len(evs2), d2)
	}
}

func TestStickyReplayOnSubscribe(t *testing.T) {
	h := New(8)
	h.PublishSticky(Event{Name: "status", Data: []byte(`{"gpu":"unavailable"}`)})
	c := h.Subscribe(nil)
	evs, _ := c.Drain()
	if len(evs) != 1 || evs[0].Name != "status" {
		t.Fatalf("sticky replay failed, got %v", evs)
	}
}

// collectSSE performs a real HTTP request against an SSE handler, gathers
// raw frames for d, then cancels.
func collectSSE(t *testing.T, url string, d time.Duration) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	var sb strings.Builder
	r := bufio.NewReader(resp.Body)
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}

func TestServeSSEFramingHeartbeatAndDropMarker(t *testing.T) {
	h := New(2)
	// Pre-load a subscription past its buffer so the first drain
	// deterministically observes 3 dropped events.
	conn := h.Subscribe(nil)
	for i := 0; i < 5; i++ {
		h.Publish(Event{Name: "tick", Data: []byte(fmt.Sprintf(`{"i":%d}`, i))})
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeConn(w, r, conn, 50*time.Millisecond)
	}))
	defer ts.Close()

	raw := collectSSE(t, ts.URL, 400*time.Millisecond)

	if !strings.Contains(raw, ": hb\n\n") {
		t.Fatalf("no heartbeat comment in stream:\n%s", raw)
	}
	if !strings.Contains(raw, "event: dropped\ndata: {\"dropped\":3}\n\n") {
		t.Fatalf("no dropped marker event in stream:\n%s", raw)
	}
	if !strings.Contains(raw, "event: tick\ndata: {\"i\":3}\n\n") ||
		!strings.Contains(raw, "event: tick\ndata: {\"i\":4}\n\n") {
		t.Fatalf("surviving events missing from stream:\n%s", raw)
	}
	if strings.Contains(raw, `{"i":0}`) || strings.Contains(raw, `{"i":1}`) || strings.Contains(raw, `{"i":2}`) {
		t.Fatalf("dropped events leaked into stream:\n%s", raw)
	}
	if !strings.HasPrefix(raw, ": connected\n\n") {
		t.Fatalf("stream does not start with the connected comment:\n%s", raw)
	}
	dropIdx := strings.Index(raw, "event: dropped")
	tickIdx := strings.Index(raw, "event: tick")
	if dropIdx == -1 || tickIdx == -1 || dropIdx > tickIdx {
		t.Fatalf("dropped marker must precede the post-drop delivery:\n%s", raw)
	}
}
