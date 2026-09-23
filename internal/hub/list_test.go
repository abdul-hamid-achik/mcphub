package hub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
)

// A downstream that pages its tool list must not be truncated to the first
// page: listAllTools follows NextCursor to the end.
func TestListAllToolsFollowsPagination(t *testing.T) {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "paged", Version: "1"},
		&mcp.ServerOptions{PageSize: 2},
	)
	for i := range 5 {
		name := string(rune('a' + i))
		server.AddTool(
			&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{}, nil
			},
		)
	}
	session := connectInMemoryClient(t, server)

	tools, err := listAllTools(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 5 {
		t.Fatalf("listAllTools returned %d tools across pages, want 5", len(tools))
	}

	list, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) >= 5 {
		t.Fatalf("test setup: downstream did not actually page (first page held %d of 5)", len(list.Tools))
	}
}

func TestListAllResourcesAndPromptsFollowPagination(t *testing.T) {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "paged-meta", Version: "1"},
		&mcp.ServerOptions{PageSize: 1},
	)
	for i := range 3 {
		server.AddResource(&mcp.Resource{URI: "test://r" + string(rune('a'+i)), Name: "r" + string(rune('a'+i))},
			func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{}, nil
			})
		server.AddPrompt(&mcp.Prompt{Name: "p" + string(rune('a'+i))},
			func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return &mcp.GetPromptResult{}, nil
			})
	}
	session := connectInMemoryClient(t, server)

	resources, err := listAllResources(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 3 {
		t.Fatalf("listAllResources returned %d, want 3", len(resources))
	}
	prompts, err := listAllPrompts(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 3 {
		t.Fatalf("listAllPrompts returned %d, want 3", len(prompts))
	}
}

// A 2026-07-28 downstream may answer a tool call with an input-required result
// (InputRequests, no content). The hub client disables the SDK's automatic
// multi-round-trip handling, so Call must convert that into a clear tool error
// instead of leaking an envelope the agent cannot answer.
func TestCallConvertsInputRequiredToToolError(t *testing.T) {
	cfg := &config.Config{}
	st, _ := openHubStore(t)
	tool := &mcp.Tool{Name: "confirm", InputSchema: map[string]any{"type": "object"}}
	server := mcp.NewServer(&mcp.Implementation{Name: "memory", Version: "1"}, nil)
	server.AddTool(tool, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			InputRequests: mcp.InputRequestMap{
				"1": &mcp.ElicitParams{Message: "Allow destructive action?"},
			},
		}, nil
	})

	h := New(cfg, st, nil)
	d := &Downstream{Name: "memory", Tools: []*mcp.Tool{tool}}
	// Connect through the hub's real client factory so the downstream session
	// carries the production options (MRTR auto-handling disabled).
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

	res, err := h.Call(context.Background(), "memory", "confirm", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("input-required result must surface as a tool error")
	}
	if len(res.InputRequests) != 0 {
		t.Fatalf("converted result must not carry the raw inputRequests envelope: %#v", res.InputRequests)
	}
	if !strings.Contains(toolErrorText(res), "interactive input") ||
		!strings.Contains(toolErrorText(res), "memory__confirm") {
		t.Fatalf("error text should name the tool and the limitation, got: %s", toolErrorText(res))
	}
}
