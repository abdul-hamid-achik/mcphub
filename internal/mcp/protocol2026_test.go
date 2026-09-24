package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/hub"
)

// The stateless HTTP listener must serve the gateway on the 2026-07-28
// protocol: no initialize handshake state, per-request _meta identity. A
// current SDK client negotiating over HTTP proves the whole surface (discover,
// tools list, tool call) works statelessly.
func TestStatelessHTTPServesGateway(t *testing.T) {
	cfg := &config.Config{Expose: config.ExposeAll, Servers: map[string]config.Server{}}
	h := hub.New(cfg, nil, nil)
	t.Cleanup(func() { _ = h.Close() })
	s := NewServer(cfg, h, nil, nil)
	if err := s.mountDownstreamTools(); err != nil {
		t.Fatal(err)
	}

	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s.srv }, &sdk.StreamableHTTPOptions{Stateless: true})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	client := sdk.NewClient(&sdk.Implementation{Name: "stateless-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(),
		&sdk.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if got := session.InitializeResult().ProtocolVersion; got != "2026-07-28" {
		t.Fatalf("stateless listener negotiated protocol %q, want 2026-07-28", got)
	}
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "mcphub_list_servers"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("list_servers failed over stateless HTTP: %s", string(res.Content[0].(*sdk.TextContent).Text))
	}
}

// A downstream that adds a prompt after connect emits prompts/list_changed;
// the hub refreshes its prompt catalog and the gateway remounts, so agents see
// the new prompt without a reconnect. The same path serves resource changes.
func TestPromptListChangedRefreshesGatewaySurface(t *testing.T) {
	downstream := sdk.NewServer(&sdk.Implementation{Name: "live", Version: "1"}, nil)
	downstream.AddTool(
		&sdk.Tool{Name: "hello", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{}, nil
		},
	)
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return downstream }, nil))
	defer httpServer.Close()

	cfg := &config.Config{
		Expose:         config.ExposeAll,
		ConnectTimeout: "2s",
		Servers: map[string]config.Server{
			"live": {URL: httpServer.URL, Transport: "http", Enabled: true},
		},
	}
	h := hub.New(cfg, nil, nil)
	// Close the hub (and its downstream sessions) before httpServer.Close: the
	// streamable client holds a long-lived stream that otherwise blocks server
	// shutdown until the remote keepalive reaps it.
	defer h.Close()
	s, cancel := dynamicMountServer(t, cfg, h, nil)
	defer cancel()
	client := connectServerClient(t, s.srv)

	assertPromptSurface(t, client, func(names map[string]int) bool {
		return len(names) == 0
	})

	downstream.AddPrompt(&sdk.Prompt{Name: "review", Description: "review a change"},
		func(context.Context, *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
			return &sdk.GetPromptResult{}, nil
		})
	assertPromptSurface(t, client, func(names map[string]int) bool {
		return names["live__review"] == 1
	})

	// The mounted prompt must forward GetPrompt to the downstream.
	res, err := client.GetPrompt(context.Background(), &sdk.GetPromptParams{Name: "live__review"})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("nil prompt result from namespaced prompt")
	}
}

func assertPromptSurface(t *testing.T, client *sdk.ClientSession, accept func(map[string]int) bool) {
	t.Helper()
	eventually(t, func() bool {
		list, err := client.ListPrompts(context.Background(), nil)
		if err != nil {
			return false
		}
		names := make(map[string]int, len(list.Prompts))
		for _, p := range list.Prompts {
			names[p.Name]++
		}
		return accept(names)
	})
}

// confirmState records both ends of an elicitation exchange: what the agent
// was asked and what the downstream received back.
type confirmState struct {
	elicitMessage atomic.Value // string the agent was asked
	answer        atomic.Value // *sdk.ElicitResult the downstream received
}

// confirmHTTPDownstream serves a tool that requires interactive input on the
// first round (a 2026-07-28 input-required result) and completes once a retry
// carries the input responses.
func confirmHTTPDownstream(t *testing.T, state *confirmState) string {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "memory", Version: "1"}, nil)
	server.AddTool(
		&sdk.Tool{Name: "confirm", Description: "confirm a destructive action", InputSchema: map[string]any{"type": "object"}},
		func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			if len(req.Params.InputResponses) == 0 {
				return &sdk.CallToolResult{
					InputRequests: sdk.InputRequestMap{
						"1": &sdk.ElicitParams{Message: "Allow the destructive action?"},
					},
				}, nil
			}
			answer, _ := req.Params.InputResponses["1"].(*sdk.ElicitResult)
			state.answer.Store(answer)
			if answer != nil && answer.Action == "accept" {
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "action executed after confirmation"}}}, nil
			}
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "action skipped: input not accepted"}}}, nil
		},
	)
	server.AddPrompt(
		&sdk.Prompt{Name: "confirm_prompt", Description: "a prompt that asks before rendering"},
		func(_ context.Context, req *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
			if len(req.Params.InputResponses) == 0 {
				return &sdk.GetPromptResult{
					InputRequests: sdk.InputRequestMap{
						"1": &sdk.ElicitParams{Message: "Render the prompt?"},
					},
				}, nil
			}
			answer, _ := req.Params.InputResponses["1"].(*sdk.ElicitResult)
			state.answer.Store(answer)
			return &sdk.GetPromptResult{Description: "rendered after confirmation"}, nil
		},
	)
	// Stateless HTTP is what serves the 2026-07-28 protocol: a stateful
	// handler would negotiate down to 2025-11-25 and elicit the hub client
	// directly (no handler) instead of returning an input-required result.
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(httpServer.Close)
	return httpServer.URL
}

// connectElicitingClient connects an agent-side client that answers every
// elicitation with action and records the message it was asked.
func connectElicitingClient(t *testing.T, server *sdk.Server, state *confirmState, action string) *sdk.ClientSession {
	t.Helper()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "agent", Version: "1"}, &sdk.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
			state.elicitMessage.Store(req.Params.Message)
			return &sdk.ElicitResult{Action: action, Content: map[string]any{"ok": true}}, nil
		},
	})
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func confirmGateway(t *testing.T, endpoint string) *Server {
	t.Helper()
	cfg := &config.Config{
		Expose:         config.ExposeAll,
		ConnectTimeout: "2s",
		Servers: map[string]config.Server{
			"memory": {URL: endpoint, Transport: "http", Enabled: true},
		},
	}
	h := hub.New(cfg, nil, nil)
	s, cancel := dynamicMountServer(t, cfg, h, nil)
	t.Cleanup(cancel)
	t.Cleanup(func() { _ = h.Close() })
	return s
}

// The full elicitation passthrough: a downstream input-required round travels
// through the gateway to the agent's elicitation handler, the answer returns
// as input responses, and the retried call completes — for directly mounted
// tools and for the lazy call_tool path alike.
func TestElicitationPassthroughMountedTool(t *testing.T) {
	var state confirmState
	s := confirmGateway(t, confirmHTTPDownstream(t, &state))
	client := connectElicitingClient(t, s.srv, &state, "accept")

	res, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "memory__confirm"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("mounted confirm call failed: %s", res.Content[0].(*sdk.TextContent).Text)
	}
	if got := state.elicitMessage.Load(); got != "Allow the destructive action?" {
		t.Fatalf("agent elicitation message = %#v, want the downstream question", got)
	}
	answer, _ := state.answer.Load().(*sdk.ElicitResult)
	if answer == nil || answer.Action != "accept" {
		t.Fatalf("downstream received answer = %#v, want accepted elicitation", answer)
	}
	if text := res.Content[0].(*sdk.TextContent).Text; text != "action executed after confirmation" {
		t.Fatalf("final result = %q", text)
	}
}

func TestElicitationPassthroughCallTool(t *testing.T) {
	var state confirmState
	s := confirmGateway(t, confirmHTTPDownstream(t, &state))
	client := connectElicitingClient(t, s.srv, &state, "decline")

	res, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "mcphub_call_tool",
		Arguments: json.RawMessage(`{"server":"memory","tool":"confirm"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("call_tool confirm failed: %s", res.Content[0].(*sdk.TextContent).Text)
	}
	answer, _ := state.answer.Load().(*sdk.ElicitResult)
	if answer == nil || answer.Action != "decline" {
		t.Fatalf("downstream received answer = %#v, want declined elicitation", answer)
	}
	if text := res.Content[0].(*sdk.TextContent).Text; text != "action skipped: input not accepted" {
		t.Fatalf("final result = %q", text)
	}
}

// Prompts relay input-required rounds like tools: the agent's answer must be
// echoed back to the downstream instead of re-asking forever.
func TestElicitationPassthroughMountedPrompt(t *testing.T) {
	var state confirmState
	s := confirmGateway(t, confirmHTTPDownstream(t, &state))
	client := connectElicitingClient(t, s.srv, &state, "accept")

	res, err := client.GetPrompt(context.Background(), &sdk.GetPromptParams{Name: "memory__confirm_prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if got := state.elicitMessage.Load(); got != "Render the prompt?" {
		t.Fatalf("agent elicitation message = %#v, want the downstream question", got)
	}
	answer, _ := state.answer.Load().(*sdk.ElicitResult)
	if answer == nil || answer.Action != "accept" {
		t.Fatalf("downstream received answer = %#v, want accepted elicitation", answer)
	}
	if res.Description != "rendered after confirmation" {
		t.Fatalf("final prompt description = %q", res.Description)
	}
}
