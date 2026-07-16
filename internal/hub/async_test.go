package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
)

// gatedDownstream builds an in-memory downstream whose tool blocks until
// release is closed (or its context is cancelled), then returns result.
func gatedDownstream(t *testing.T, release <-chan struct{}, result *mcp.CallToolResult) (*mcp.ClientSession, *mcp.Tool) {
	t.Helper()
	tool := &mcp.Tool{Name: "slow", InputSchema: map[string]any{"type": "object"}}
	server := mcp.NewServer(&mcp.Implementation{Name: "gated", Version: "1"}, nil)
	server.AddTool(tool, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		select {
		case <-release:
			return result, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	return connectInMemoryClient(t, server), tool
}

// waitDetachedStatus polls until the detached call reaches want or a bounded
// deadline expires.
func waitDetachedStatus(t *testing.T, h *Hub, id string, want DetachedStatus) DetachedCall {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		call, ok := h.PollDetached(id)
		if !ok {
			t.Fatalf("detached call %s disappeared while waiting for %s", id, want)
		}
		if call.Status == want {
			return call
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("detached call %s never reached status %s", id, want)
	return DetachedCall{}
}

func TestStartDetachedValidation(t *testing.T) {
	h := New(&config.Config{}, nil, nil)
	h.downstreams = []*Downstream{
		{Name: "dead", Err: errors.New("boom")},
		{Name: "live", Tools: []*mcp.Tool{{Name: "echo"}}}, // no session => not connected
	}
	ctx := context.Background()
	if _, err := h.StartDetached(ctx, "ghost", "t", nil, time.Minute); err == nil || !strings.Contains(err.Error(), "unknown server") {
		t.Fatalf("unknown server: got %v", err)
	}
	if _, err := h.StartDetached(ctx, "dead", "t", nil, time.Minute); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("not connected: got %v", err)
	}
	if _, ok := h.PollDetached("never-issued"); ok {
		t.Fatal("PollDetached must miss an ID that was never issued")
	}
}

func TestDetachedCallSurvivesCallerCancelAndPollsDone(t *testing.T) {
	release := make(chan struct{})
	want := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "background done"}}}
	session, tool := gatedDownstream(t, release, want)
	h := New(&config.Config{}, nil, nil)
	h.downstreams = []*Downstream{{Name: "bg", session: session, Tools: []*mcp.Tool{tool}}}

	ctx, cancel := context.WithCancel(context.Background())
	id, err := h.StartDetached(ctx, "bg", "slow", nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// The requesting client going away must not kill the background call.
	cancel()

	call, ok := h.PollDetached(id)
	if !ok || call.Status != DetachedPending || call.Namespaced != "bg__slow" {
		t.Fatalf("pending snapshot = %+v ok=%v", call, ok)
	}
	if call.Result != nil || call.Err != "" || !call.CompletedAt.IsZero() {
		t.Fatalf("pending call carries completion state: %+v", call)
	}

	close(release)
	done := waitDetachedStatus(t, h, id, DetachedDone)
	if done.Err != "" || done.Result == nil {
		t.Fatalf("done snapshot = %+v", done)
	}
	if text := done.Result.Content[0].(*mcp.TextContent).Text; text != "background done" {
		t.Fatalf("done result text = %q", text)
	}
	if done.CompletedAt.IsZero() {
		t.Fatal("done call has no CompletedAt")
	}

	// Polling a completed call is idempotent until retention lapses.
	again, ok := h.PollDetached(id)
	if !ok || again.Status != DetachedDone || again.Result != done.Result {
		t.Fatalf("re-poll = %+v ok=%v", again, ok)
	}
}

func TestDetachedCallTimeoutBecomesFailed(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	session, tool := gatedDownstream(t, release, &mcp.CallToolResult{})
	h := New(&config.Config{}, nil, nil)
	h.downstreams = []*Downstream{{Name: "bg", session: session, Tools: []*mcp.Tool{tool}}}

	id, err := h.StartDetached(context.Background(), "bg", "slow", nil, 30*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitDetachedStatus(t, h, id, DetachedFailed)
	if failed.Err == "" || !strings.Contains(failed.Err, "outcome unknown") {
		t.Fatalf("failed.Err = %q, want the hub's outcome-unknown transport error", failed.Err)
	}
	if failed.Result != nil {
		t.Fatalf("failed call carries a result: %+v", failed.Result)
	}
}

func TestStartDetachedEnforcesPendingCap(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	session, tool := gatedDownstream(t, release, &mcp.CallToolResult{})
	h := New(&config.Config{}, nil, nil)
	h.downstreams = []*Downstream{{Name: "bg", session: session, Tools: []*mcp.Tool{tool}}}

	h.detachedMu.Lock()
	h.detached = map[string]*DetachedCall{}
	for i := 0; i < maxDetachedPending; i++ {
		id := fmt.Sprintf("pending-%d", i)
		h.detached[id] = &DetachedCall{ID: id, Status: DetachedPending, StartedAt: h.now()}
	}
	h.detachedMu.Unlock()

	if _, err := h.StartDetached(context.Background(), "bg", "slow", nil, time.Minute); err == nil || !strings.Contains(err.Error(), "in flight") {
		t.Fatalf("over-cap StartDetached error = %v", err)
	}
}

func TestDetachedRetentionPrunesExpiredAndEvictsOldest(t *testing.T) {
	h := New(&config.Config{}, nil, nil)
	base := time.Now()
	var mu sync.Mutex
	current := base
	h.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return current
	}
	setNow := func(v time.Time) {
		mu.Lock()
		defer mu.Unlock()
		current = v
	}

	// An expired completed entry is dropped once the TTL lapses.
	h.detachedMu.Lock()
	h.detached = map[string]*DetachedCall{
		"old": {ID: "old", Status: DetachedDone, StartedAt: base, CompletedAt: base},
	}
	h.detachedMu.Unlock()
	if _, ok := h.PollDetached("old"); !ok {
		t.Fatal("fresh completed entry should still be pollable")
	}
	setNow(base.Add(detachedResultTTL + time.Minute))
	if _, ok := h.PollDetached("old"); ok {
		t.Fatal("expired completed entry survived its retention window")
	}

	// Beyond the completed cap, the oldest-finished entries are evicted while
	// pending entries are untouched.
	setNow(base)
	h.detachedMu.Lock()
	h.detached = map[string]*DetachedCall{
		"running": {ID: "running", Status: DetachedPending, StartedAt: base},
	}
	overflow := 5
	for i := 0; i < maxDetachedCompleted+overflow; i++ {
		id := fmt.Sprintf("done-%03d", i)
		h.detached[id] = &DetachedCall{
			ID: id, Status: DetachedDone,
			StartedAt: base, CompletedAt: base.Add(time.Duration(i) * time.Second),
		}
	}
	h.detachedMu.Unlock()

	if _, ok := h.PollDetached("missing"); ok { // any poll triggers pruning
		t.Fatal("unexpected hit")
	}
	for i := 0; i < overflow; i++ {
		if _, ok := h.PollDetached(fmt.Sprintf("done-%03d", i)); ok {
			t.Fatalf("oldest completed entry done-%03d was not evicted", i)
		}
	}
	if _, ok := h.PollDetached(fmt.Sprintf("done-%03d", maxDetachedCompleted+overflow-1)); !ok {
		t.Fatal("newest completed entry was evicted")
	}
	if _, ok := h.PollDetached("running"); !ok {
		t.Fatal("pending entry must never be pruned")
	}
}

func TestDetachedOversizedResultReturnsSpooledReceipt(t *testing.T) {
	st, _ := openHubStore(t)
	cfg := &config.Config{ResponseBudget: "900B"}
	original := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("bg-data-", 600)}}}
	expected, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	session, tool := gatedDownstream(t, release, original)
	h := New(cfg, st, nil)
	h.downstreams = []*Downstream{{Name: "bg", session: session, Tools: []*mcp.Tool{tool}}}

	id, err := h.StartDetached(context.Background(), "bg", "slow", nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	done := waitDetachedStatus(t, h, id, DetachedDone)
	receipt, ok := done.Result.StructuredContent.(resultReceipt)
	if !ok || receipt.CallID == "" {
		t.Fatalf("oversized detached result did not finalize into a receipt: %#v", done.Result.StructuredContent)
	}
	var rebuilt []byte
	var cursor int64
	for {
		page, err := st.ReadResultPage(context.Background(), receipt.CallID, cursor, 257)
		if err != nil {
			t.Fatal(err)
		}
		rebuilt = append(rebuilt, page.Data...)
		cursor = page.NextCursor
		if page.Done {
			break
		}
	}
	if !bytes.Equal(rebuilt, expected) {
		t.Fatal("spooled detached result did not reconstruct byte-for-byte")
	}
}
