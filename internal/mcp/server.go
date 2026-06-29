// Package mcp is mcphub's own MCP stdio server — the single endpoint every
// agent harness points at. It exposes a handful of management/introspection
// tools (list servers, stats, search the aggregated tool catalog) and then
// mounts every downstream tool the hub aggregates, so one connection fronts
// them all.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/hub"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
	"github.com/abdul-hamid-achik/mcphub/internal/version"
)

// Server is the mcphub gateway MCP server.
type Server struct {
	srv   *sdk.Server
	hub   *hub.Hub
	store *store.Store
	cfg   *config.Config
}

// NewServer builds the gateway server. The hub must already be connected (or
// will be connected by Run) so its tools can be mounted.
func NewServer(cfg *config.Config, h *hub.Hub, st *store.Store) *Server {
	impl := &sdk.Implementation{Name: "mcphub", Version: version.Version}
	instructions := "mcphub is a gateway that fronts many MCP servers behind one connection. " +
		"Downstream tools are exposed as `server__tool`. Use mcphub_list_servers to see what is " +
		"connected, mcphub_search_tools to find a capability, and mcphub_stats to inspect local " +
		"usage intelligence (calls, latency, token cost per server)."
	if cfg.Lazy() {
		instructions += " IMPORTANT: this gateway is in LAZY mode — the underlying tools are " +
			"intentionally not listed to save context, but they ARE available. Whenever a task " +
			"could use an external capability (code search, secrets, browser/TUI testing, system " +
			"info, docs, ...), take the initiative: call mcphub_search_tools with a short query to " +
			"find the right `server__tool`, then run it with mcphub_call_tool {server, tool, " +
			"arguments}. Do this proactively without being asked; use mcphub_describe_tool first if " +
			"you need a tool's input schema."
		if len(cfg.Pin) > 0 {
			instructions += " Some frequently-used tools are pinned and listed directly — call those by name as usual."
		}
	}
	opts := &sdk.ServerOptions{Instructions: instructions}
	s := &Server{srv: sdk.NewServer(impl, opts), hub: h, store: st, cfg: cfg}
	s.registerManagement()
	return s
}

// Run connects the hub, mounts the aggregated tools (all of them unless lazy;
// just the pinned ones in lazy mode), and serves on stdio.
func (s *Server) Run(ctx context.Context) error {
	s.hub.Connect(ctx)
	defer s.hub.Close()
	// Background watcher: reconnect downstreams that fail or die mid-session,
	// so a crashed server self-heals without restarting the agent.
	go s.hub.Watch(ctx)
	if s.cfg.Lazy() {
		// Lazy: advertise only the meta-tools, plus any pinned tools so the
		// agent's most-used tools stay directly callable. Pins may name a whole
		// server, a `server__*` wildcard, or an exact `server__tool`.
		if len(s.cfg.Pin) > 0 {
			s.hub.MountMatching(s.srv, s.cfg.PinMatches)
		}
	} else {
		s.hub.Mount(s.srv)
	}
	return s.srv.Run(ctx, &sdk.StdioTransport{})
}

func (s *Server) registerManagement() {
	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "mcphub_list_servers",
		Description: "List configured downstream servers with enabled/connected state and tool counts.",
	}, s.handleListServers)

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "mcphub_search_tools",
		Description: "Search the aggregated tool catalog by substring across name and description. Returns matching `server__tool` names so you can call them via mcphub_call_tool without loading every tool.",
	}, s.handleSearchTools)

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "mcphub_describe_tool",
		Description: "Return a downstream tool's description and full JSON input schema, so you can construct a valid mcphub_call_tool request.",
	}, s.handleDescribeTool)

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "mcphub_call_tool",
		Description: "Invoke a downstream tool by name and return its result verbatim. Accepts {server, tool, arguments} (tool may be the combined `server__tool` form). This is how you call tools in lazy mode.",
	}, s.handleCallTool)

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "mcphub_stats",
		Description: "Return local usage intelligence: total calls, error count, estimated token cost, and per-server breakdown recorded by the gateway.",
	}, s.handleStats)
}

// splitNamespaced resolves (server, tool) from explicit fields, falling back to
// splitting a combined `server__tool` name on the first `__`. When server is
// explicitly provided, a redundant `server__` prefix on tool is stripped — so
// an agent that echoes the combined `namespaced` field from mcphub_search_tools
// into tool while also setting server still routes correctly.
func splitNamespaced(server, tool string) (string, string) {
	if server == "" {
		if i := strings.Index(tool, "__"); i >= 0 {
			return tool[:i], tool[i+2:]
		}
		return server, tool
	}
	return server, strings.TrimPrefix(tool, server+"__")
}

// --- inputs ---------------------------------------------------------------

type emptyInput struct{}

type searchInput struct {
	Query string `json:"query" jsonschema:"substring to match against tool name and description"`
}

type describeInput struct {
	Server string `json:"server,omitempty" jsonschema:"downstream server name (optional if tool is server__tool)"`
	Tool   string `json:"tool" jsonschema:"tool name; may be the combined server__tool form"`
}

type callInput struct {
	Server    string         `json:"server,omitempty" jsonschema:"downstream server name (optional if tool is server__tool)"`
	Tool      string         `json:"tool" jsonschema:"tool name; may be the combined server__tool form"`
	Arguments map[string]any `json:"arguments,omitempty" jsonschema:"arguments object passed to the downstream tool"`
}

// --- handlers -------------------------------------------------------------

type serverInfo struct {
	Name        string   `json:"name"`
	Enabled     bool     `json:"enabled"`
	Connected   bool     `json:"connected"`
	Tools       int      `json:"tools"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Error       string   `json:"error,omitempty"`
}

func (s *Server) handleListServers(_ context.Context, _ *sdk.CallToolRequest, _ emptyInput) (*sdk.CallToolResult, any, error) {
	state := map[string]*hub.Downstream{}
	for _, d := range s.hub.Downstreams() {
		state[d.Name] = d
	}
	out := make([]serverInfo, 0, len(s.cfg.Servers))
	for _, name := range s.cfg.ServerNames() {
		srv := s.cfg.Servers[name]
		info := serverInfo{
			Name:        name,
			Enabled:     srv.Enabled,
			Description: srv.Description,
			Tags:        srv.Tags,
		}
		if d, ok := state[name]; ok {
			info.Connected = d.Connected()
			info.Tools = len(d.Tools)
			if d.Err != nil {
				info.Error = d.Err.Error()
			}
		}
		out = append(out, info)
	}
	expose := config.ExposeAll
	if s.cfg.Lazy() {
		expose = config.ExposeLazy
	}
	return result(map[string]any{"servers": out, "total_tools": s.hub.ToolCount(), "expose": expose, "pinned": s.cfg.Pin})
}

type toolMatch struct {
	Namespaced  string `json:"namespaced"`
	Server      string `json:"server"`
	Tool        string `json:"tool"`
	Description string `json:"description"`
}

func (s *Server) handleSearchTools(_ context.Context, _ *sdk.CallToolRequest, in searchInput) (*sdk.CallToolResult, any, error) {
	q := strings.ToLower(strings.TrimSpace(in.Query))
	var matches []toolMatch
	for _, d := range s.hub.Downstreams() {
		if !d.Connected() {
			continue
		}
		for _, t := range d.Tools {
			ns := d.Name + "__" + t.Name
			if q == "" || strings.Contains(strings.ToLower(ns), q) || strings.Contains(strings.ToLower(t.Description), q) {
				matches = append(matches, toolMatch{Namespaced: ns, Server: d.Name, Tool: t.Name, Description: t.Description})
			}
		}
	}
	return result(map[string]any{"query": in.Query, "count": len(matches), "matches": matches})
}

func (s *Server) handleDescribeTool(_ context.Context, _ *sdk.CallToolRequest, in describeInput) (*sdk.CallToolResult, any, error) {
	server, tool := splitNamespaced(in.Server, in.Tool)
	if server == "" || tool == "" {
		return result(map[string]any{"error": "need server and tool (or a server__tool name)"})
	}
	t, ok := s.hub.FindTool(server, tool)
	if !ok {
		return result(map[string]any{"error": "tool not found", "server": server, "tool": tool})
	}
	return result(map[string]any{
		"server":       server,
		"tool":         tool,
		"namespaced":   server + "__" + tool,
		"description":  t.Description,
		"input_schema": t.InputSchema,
	})
}

func (s *Server) handleCallTool(ctx context.Context, _ *sdk.CallToolRequest, in callInput) (*sdk.CallToolResult, any, error) {
	server, tool := splitNamespaced(in.Server, in.Tool)
	if server == "" || tool == "" {
		return nil, nil, fmt.Errorf("need server and tool (or a server__tool name)")
	}
	var args json.RawMessage
	if in.Arguments != nil {
		b, err := json.Marshal(in.Arguments)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal arguments: %w", err)
		}
		args = b
	}
	res, err := s.hub.Call(ctx, server, tool, args)
	if err != nil {
		return nil, nil, fmt.Errorf("call %s__%s: %w", server, tool, err)
	}
	// Pass the downstream result through verbatim.
	return res, nil, nil
}

func (s *Server) handleStats(ctx context.Context, _ *sdk.CallToolRequest, _ emptyInput) (*sdk.CallToolResult, any, error) {
	if s.store == nil {
		return result(map[string]any{"error": "telemetry store not configured"})
	}
	totals, err := s.store.Totals(ctx)
	if err != nil {
		return result(map[string]any{"error": err.Error()})
	}
	servers, err := s.store.ServerStats(ctx)
	if err != nil {
		return result(map[string]any{"error": err.Error()})
	}
	return result(map[string]any{"totals": totals, "servers": servers})
}

// result mirrors the codemap/monitor convention: a JSON text block plus the
// same value as structured output.
func result(v any) (*sdk.CallToolResult, any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal result: %w", err)
	}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}, v, nil
}
