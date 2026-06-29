package hub

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
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

func TestTransportFor(t *testing.T) {
	cases := []struct {
		name string
		srv  config.Server
		want func(mcp.Transport) bool
	}{
		{
			"stdio",
			config.Server{Command: "echo", Args: []string{"hi"}},
			func(tr mcp.Transport) bool {
				ct, ok := tr.(*mcp.CommandTransport)
				return ok && ct.Command != nil && ct.Command.Args[0] == "echo"
			},
		},
		{
			"http remote",
			config.Server{URL: "https://srv.example.com", Transport: "http"},
			func(tr mcp.Transport) bool {
				st, ok := tr.(*mcp.StreamableClientTransport)
				return ok && st.Endpoint == "https://srv.example.com"
			},
		},
		{
			"sse remote",
			config.Server{URL: "https://sse.example.com", Transport: "sse"},
			func(tr mcp.Transport) bool {
				st, ok := tr.(*mcp.SSEClientTransport)
				return ok && st.Endpoint == "https://sse.example.com"
			},
		},
		{
			"default remote (unset transport → http)",
			config.Server{URL: "https://def.example.com"},
			func(tr mcp.Transport) bool {
				_, ok := tr.(*mcp.StreamableClientTransport)
				return ok
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := transportFor(c.srv)
			if !c.want(tr) {
				t.Fatalf("transportFor(%+v) = %T, wrong type or field", c.srv, tr)
			}
		})
	}
}

func TestTransportForVaultWrapped(t *testing.T) {
	srv := config.Server{Command: "myserver", Args: []string{"--port", "8080"}, Vault: "secrets"}
	tr := transportFor(srv)
	ct, ok := tr.(*mcp.CommandTransport)
	if !ok {
		t.Fatalf("expected *CommandTransport, got %T", tr)
	}
	if ct.Command.Args[0] != "tvault" {
		t.Fatalf("expected tvault wrapper, got %q", ct.Command.Args[0])
	}
	// tvault run --project secrets -- myserver --port 8080
	wantArgs := []string{"tvault", "run", "--project", "secrets", "--", "myserver", "--port", "8080"}
	if !reflect.DeepEqual(ct.Command.Args, wantArgs) {
		t.Fatalf("vault args = %v, want %v", ct.Command.Args, wantArgs)
	}
}
