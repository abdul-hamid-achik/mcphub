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
	"sort"
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
	scope *agentScope // nil = unscoped (advertise everything)
}

// NewServer builds the gateway server. The hub must already be connected (or
func NewServer(cfg *config.Config, h *hub.Hub, st *store.Store, scope *agentScope) *Server {
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
	s := &Server{srv: sdk.NewServer(impl, opts), hub: h, store: st, cfg: cfg, scope: scope}
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
		// server, a `server__*` wildcard, or an exact `server__tool`. A pin
		// outside this agent's scope is silently skipped.
		if len(s.cfg.Pin) > 0 {
			s.hub.MountMatching(s.srv, func(ns string) bool {
				return s.cfg.PinMatches(ns) && s.scope.allowsNS(ns)
			})
		}
	} else {
		// expose: all — mount every downstream tool the agent's scope permits
		// (nil scope = everything, the unscoped default).
		s.hub.MountMatching(s.srv, s.scope.allowsNS)
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
		Name:        "mcphub_resolve_tool",
		Description: "Find the best tool for a task and return it with required fields + an argument template, so you can call it directly without separate search + describe steps. Returns one recommendation, alternatives, and an ambiguity flag.",
	}, s.handleResolveTool)

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

type resolveToolInput struct {
	Query   string `json:"query" jsonschema:"natural-language description of what you want to do"`
	MaxHits int    `json:"max_hits,omitempty" jsonschema:"max alternatives to return (default 5)"`
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
		if !s.scope.allowsServer(name) {
			continue
		}
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
		if !d.Connected() || !s.scope.allowsServer(d.Name) {
			continue
		}
		for _, t := range d.Tools {
			ns := d.Name + "__" + t.Name
			if !s.scope.allowsNS(ns) {
				continue
			}
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
	if !s.scope.allows(server, tool) {
		return nil, nil, fmt.Errorf("tool %s__%s is out of scope for this agent", server, tool)
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

func (s *Server) handleResolveTool(_ context.Context, _ *sdk.CallToolRequest, in resolveToolInput) (*sdk.CallToolResult, any, error) {
	q := strings.ToLower(strings.TrimSpace(in.Query))
	maxHits := in.MaxHits
	if maxHits <= 0 || maxHits > 10 {
		maxHits = 5
	}
	var matches []toolMatch
	for _, d := range s.hub.Downstreams() {
		if !d.Connected() || !s.scope.allowsServer(d.Name) {
			continue
		}
		for _, t := range d.Tools {
			ns := d.Name + "__" + t.Name
			if !s.scope.allowsNS(ns) {
				continue
			}
			if q == "" || strings.Contains(strings.ToLower(ns), q) || strings.Contains(strings.ToLower(t.Name), q) || strings.Contains(strings.ToLower(t.Description), q) {
				matches = append(matches, toolMatch{Namespaced: ns, Server: d.Name, Tool: t.Name, Description: t.Description})
			}
		}
	}
	if len(matches) == 0 {
		return result(map[string]any{"query": in.Query, "recommendation": nil, "ambiguous": false, "alternatives": []toolMatch{}, "hint": "no tools matched — try a broader query or use mcphub_search_tools"})
	}
	// Rank: exact name match > name substring > description substring.
	sort.Slice(matches, func(i, j int) bool {
		return resolveRank(q, matches[i]) > resolveRank(q, matches[j])
	})
	top := matches[0]
	alts := matches[1:]
	if len(alts) > maxHits {
		alts = alts[:maxHits]
	}
	ambiguous := len(matches) > 1 && resolveRank(q, top) == resolveRank(q, matches[1])
	// Extract required fields + build an argument template from the tool's schema.
	t, ok := s.hub.FindTool(top.Server, top.Tool)
	required, template := []string{}, map[string]any{}
	if ok && t.InputSchema != nil {
		var schema map[string]any
		// InputSchema is `any` — marshal then unmarshal to normalize.
		b, mErr := json.Marshal(t.InputSchema)
		if mErr == nil {
			json.Unmarshal(b, &schema)
		}
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if s, ok := r.(string); ok {
					required = append(required, s)
					template[s] = "<value>"
				}
			}
		}
		if props, ok := schema["properties"].(map[string]any); ok {
			for k := range props {
				if _, exists := template[k]; !exists {
					template[k] = nil
				}
			}
		}
	}
	return result(map[string]any{
		"query": in.Query,
		"recommendation": map[string]any{
			"server":            top.Server,
			"tool":              top.Tool,
			"namespaced":        top.Namespaced,
			"description":       top.Description,
			"required_fields":   required,
			"argument_template": template,
		},
		"ambiguous":    ambiguous,
		"alternatives": alts,
		"hint":         resolveHint(ambiguous, top.Namespaced),
	})
}

// resolveRank scores a match: 3 = exact name, 2 = name substring, 1 = description only.
func resolveRank(q string, m toolMatch) int {
	nameLower := strings.ToLower(m.Tool)
	switch {
	case nameLower == q:
		return 3
	case strings.Contains(nameLower, q):
		return 2
	default:
		return 1
	}
}

func resolveHint(ambiguous bool, namespaced string) string {
	if ambiguous {
		return "multiple tools ranked equally — review the alternatives and pick the one whose description best matches your intent"
	}
	return "call mcphub_call_tool with server + tool (or the namespaced name) + the argument_template filled in"
}

func (s *Server) handleCallTool(ctx context.Context, _ *sdk.CallToolRequest, in callInput) (*sdk.CallToolResult, any, error) {
	server, tool := splitNamespaced(in.Server, in.Tool)
	if server == "" || tool == "" {
		return nil, nil, fmt.Errorf("need server and tool (or a server__tool name)")
	}
	if !s.scope.allows(server, tool) {
		return nil, nil, fmt.Errorf("tool %s__%s is out of scope for this agent", server, tool)
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
	// Apply the response budget (SPEC §8.2: bounded lossless gateway results).
	// Verbatim mode or budget=0 passes the result through untouched.
	if !s.cfg.Verbatim {
		budget := s.cfg.ResponseBudgetBytes()
		if budget > 0 {
			res = truncateResult(res, budget, server+"__"+tool)
		}
	}
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

// truncateResult caps a CallToolResult's text content to fit within a byte
// budget (SPEC §8.2). When truncation occurs, a notice is appended so the
// agent knows the result was capped and can re-run with verbatim=true if it
// needs the full output.
func truncateResult(res *sdk.CallToolResult, budget int, namespaced string) *sdk.CallToolResult {
	totalSize := 0
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			totalSize += len(tc.Text)
		}
	}
	if totalSize <= budget {
		return res
	}
	// Truncate proportionally across text content blocks.
	budgetUsed := 0
	notice := fmt.Sprintf("\n\n[result truncated: original %d bytes, budget %d bytes - set verbatim=true in mcphub.yaml to opt out]", totalSize, budget)
	budget -= len(notice)
	for i, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			remaining := budget - budgetUsed
			if remaining <= 0 {
				res.Content[i] = &sdk.TextContent{Text: notice}
				budgetUsed += len(notice)
				break
			}
			if len(tc.Text) > remaining {
				res.Content[i] = &sdk.TextContent{Text: tc.Text[:remaining] + notice}
				budgetUsed = budget + len(notice)
				break
			}
			budgetUsed += len(tc.Text)
		}
	}
	return res
}
