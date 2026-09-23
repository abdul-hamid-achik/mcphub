package hub

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
)

// A downstream that answers a tool call with a JSON-RPC error (a protocol
// refusal — invalid params, an unfulfillable elicitation, a server bug — as
// opposed to a lost connection) must fail only that call: the session stays
// valid and later calls reuse it. The outcome-unknown and reconnect machinery
// is for transport failures only.
func TestCallProtocolErrorKeepsSession(t *testing.T) {
	st, _ := openHubStore(t)
	var calls atomic.Int32
	tool := &mcp.Tool{Name: "refuse", InputSchema: map[string]any{"type": "object"}}
	server := mcp.NewServer(&mcp.Implementation{Name: "memory", Version: "1"}, nil)
	server.AddTool(tool, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if calls.Add(1) == 1 {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "refused: bad arguments"}
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "fine on retry"}}}, nil
	})

	h := New(&config.Config{}, st, nil)
	d := &Downstream{Name: "memory", Tools: []*mcp.Tool{tool}}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "mcphub", Version: "1"}, h.downstreamClientOptions(false, d))
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	d.session = session
	h.downstreams = []*Downstream{d}

	_, err = h.Call(context.Background(), "memory", "refuse", nil)
	if err == nil {
		t.Fatal("refused call must return an error")
	}
	if !strings.Contains(err.Error(), "refused: bad arguments") {
		t.Fatalf("error should carry the downstream refusal, got: %v", err)
	}
	if strings.Contains(err.Error(), "outcome unknown") {
		t.Fatalf("protocol refusal must not be reported as outcome unknown: %v", err)
	}
	var jsonrpcErr *jsonrpc.Error
	if !errors.As(err, &jsonrpcErr) {
		t.Fatalf("error should unwrap to the JSON-RPC error, got: %v", err)
	}
	if !d.Connected() {
		t.Fatal("protocol refusal must not invalidate the downstream session")
	}
	res, err := h.Call(context.Background(), "memory", "refuse", nil)
	if err != nil {
		t.Fatalf("next call must reuse the live session: %v", err)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; text != "fine on retry" {
		t.Fatalf("second call result = %q", text)
	}
	if calls.Load() != 2 {
		t.Fatalf("downstream saw %d calls, want 2 (no reconnect in between)", calls.Load())
	}
}
