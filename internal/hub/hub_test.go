package hub

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
)

func TestCallAndFindTool(t *testing.T) {
	h := New(&config.Config{}, nil, nil) // nil store => telemetry skipped
	h.downstreams = []*Downstream{
		{Name: "dead", Err: errors.New("boom")},            // Connected()==false
		{Name: "live", Tools: []*mcp.Tool{{Name: "echo"}}}, // no session, but Tools set
	}
	ctx := context.Background()

	if _, err := h.Call(ctx, "ghost", "t", nil); err == nil || !strings.Contains(err.Error(), "unknown server") {
		t.Fatalf("unknown server: got %v", err)
	}
	if _, err := h.Call(ctx, "dead", "t", nil); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("not connected: got %v", err)
	}
	if _, ok := h.FindTool("ghost", "t"); ok {
		t.Error("FindTool should miss an unknown server")
	}
	if _, ok := h.FindTool("live", "nope"); ok {
		t.Error("FindTool should miss an unknown tool")
	}
	if tool, ok := h.FindTool("live", "echo"); !ok || tool.Name != "echo" {
		t.Fatalf("FindTool should find echo, got %v ok=%v", tool, ok)
	}

	// record() with a nil store must be a no-op and never panic.
	h.record(ctx, "live", "echo", "live__echo", 0, nil, 0, nil)
}

func recordingHub(t *testing.T) (*Hub, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(&config.Config{}, st, nil), st
}

// TestRecordCountsToolError pins finding-11: a tool that returns IsError (with
// no transport error) must be recorded as an error, not a success.
func TestRecordCountsToolError(t *testing.T) {
	h, st := recordingHub(t)
	ctx := context.Background()
	h.record(ctx, "s", "t", "s__t", time.Millisecond, nil, 10, &mcp.CallToolResult{})
	h.record(ctx, "s", "t", "s__t", time.Millisecond, nil, 10,
		&mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "boom"}}})

	tot, err := st.Totals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tot.Calls != 2 {
		t.Errorf("calls = %d, want 2", tot.Calls)
	}
	if tot.Errors != 1 {
		t.Errorf("an IsError tool result must count as 1 error, got %d", tot.Errors)
	}
}

// TestRecordSurvivesCancelledContext pins finding-10: a cancelled/timed-out call
// is exactly the one worth recording, so the telemetry write must not ride the
// caller's cancelled context.
func TestRecordSurvivesCancelledContext(t *testing.T) {
	h, st := recordingHub(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled, as on an aborted/timed-out tool call
	h.record(ctx, "s", "t", "s__t", time.Millisecond, context.Canceled, 0, nil)

	tot, err := st.Totals(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tot.Calls != 1 {
		t.Errorf("a cancelled-context call should still be recorded, got %d", tot.Calls)
	}
	if tot.Errors != 1 {
		t.Errorf("the cancelled call should be recorded as an error, got %d", tot.Errors)
	}
}
