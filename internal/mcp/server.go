// Package mcp is mcphub's own MCP stdio server — the single endpoint every
// agent harness points at. It exposes a handful of management/introspection
// tools (list servers, stats, search the aggregated tool catalog) and then
// mounts every downstream tool the hub aggregates, so one connection fronts
// them all.
package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/hub"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
	"github.com/abdul-hamid-achik/mcphub/internal/version"
)

// Server is the mcphub gateway MCP server.
type Server struct {
	srv                       *sdk.Server
	hub                       *hub.Hub
	store                     *store.Store
	cfg                       *config.Config
	scope                     *agentScope // nil = unscoped (advertise everything)
	mountMu                   sync.RWMutex
	mountedDownstreamTools    map[string][sha256.Size]byte
	advertisedDownstreamTools int
	toolMountReport           *hub.ToolMountReport
}

const managementToolCount = 8

// NewServer builds the gateway server and registers mcphub's management tools.
// Run connects the downstream hub before it mounts or serves any proxy tools.
func NewServer(cfg *config.Config, h *hub.Hub, st *store.Store, scope *agentScope) *Server {
	impl := &sdk.Implementation{Name: "mcphub", Version: version.Version}
	instructions := "mcphub is a gateway that fronts many MCP servers behind one connection. " +
		"Downstream tools are exposed as `server__tool`. Use mcphub_list_servers to see what is " +
		"connected, mcphub_resolve_tool to route current task context to a capability, " +
		"mcphub_search_tools to browse alternatives, and mcphub_stats to inspect local " +
		"usage intelligence (calls, latency, token cost per server)."
	if cfg.Lazy() {
		instructions += " IMPORTANT: this gateway is in LAZY mode — the underlying tools are " +
			"intentionally not listed to save context, but they ARE available. At the start of a " +
			"non-trivial task and whenever work changes phase (research, planning, implementation, " +
			"verification), proactively call mcphub_resolve_tool with the current goal or activity. " +
			"It accepts natural language and returns a ranked `server__tool` plus a ready-to-fill " +
			"argument template. Run the choice with mcphub_call_tool {server, tool, arguments} only " +
			"when status is `confident` and `ambiguous` is false. Never auto-run an ambiguous " +
			"recommendation; compare alternatives, search for another tool, or ask for direction. " +
			"Use mcphub_search_tools to browse more candidates. Do this without waiting to be asked."
		if summary := capabilitySummary(cfg, scope); summary != "" {
			instructions += " Capability families configured for this agent (call mcphub_list_servers for live status): " + summary + "."
		}
		if len(scope.effectivePins(cfg)) > 0 {
			instructions += " Some frequently-used tools are pinned and listed directly — call those by name as usual."
		}
	}
	if budget, configured := scope.schemaBudget(); configured {
		instructions += fmt.Sprintf(
			" This agent caps directly advertised downstream tool definitions at %d serialized bytes; "+
				"the %d mcphub management tools remain listed, and omitted in-scope tools remain callable through mcphub_call_tool.",
			budget, managementToolCount,
		)
	}
	opts := &sdk.ServerOptions{Instructions: instructions}
	s := &Server{srv: sdk.NewServer(impl, opts), hub: h, store: st, cfg: cfg, scope: scope}
	s.registerManagement()
	return s
}

// Run connects only the servers admitted by this gateway's agent scope, mounts
// the aggregated tools (all of them unless lazy; just the pinned ones in lazy
// mode), and serves on stdio. Applying scope before Connect keeps excluded
// commands, remote connections, and secret resolution inactive.
func (s *Server) Run(ctx context.Context) error {
	changes, unsubscribe := s.hub.SubscribeChanges()
	defer unsubscribe()
	s.hub.ConnectMatching(ctx, s.scope.allowsServer)
	defer s.hub.Close()
	if err := s.mountDownstreamTools(); err != nil {
		return fmt.Errorf("mount downstream tools: %w", err)
	}
	// Background watcher: reconnect downstreams that fail or die mid-session,
	// so a crashed server self-heals without restarting the agent.
	watchCtx, cancelWatchers := context.WithCancel(ctx)
	defer cancelWatchers()
	go s.hub.Watch(watchCtx)
	go s.watchDownstreamTools(watchCtx, changes)
	return s.srv.Run(ctx, &sdk.StdioTransport{})
}

// mountDownstreamTools applies agent authorization, per-agent pin selection,
// and the optional serialized-definition budget before tools/list can observe
// any downstream definitions. Management tools are registered separately in
// NewServer and therefore can never be removed by this admission pass.
func (s *Server) mountDownstreamTools() error {
	s.mountMu.Lock()
	defer s.mountMu.Unlock()

	want := s.scope.allowsNS
	if s.cfg.Lazy() {
		// Lazy: advertise only the meta-tools, plus any pinned tools so the
		// agent's most-used tools stay directly callable. Pins may name a whole
		// server, a `server__*` wildcard, or an exact `server__tool`. A pin
		// outside this agent's scope is silently skipped.
		want = func(ns string) bool {
			return s.scope.pinMatches(s.cfg, ns) && s.scope.allowsNS(ns)
		}
	}
	var (
		mounts []hub.ToolMount
		report *hub.ToolMountReport
	)
	if budget, configured := s.scope.schemaBudget(); configured {
		planned, r, err := s.hub.MatchingToolsBudgeted(want, budget)
		if err != nil {
			return err
		}
		mounts = planned
		report = &r
	} else {
		mounts = s.hub.MatchingTools(want)
	}

	desired := make(map[string][sha256.Size]byte, len(mounts))
	for _, mount := range mounts {
		encoded, err := json.Marshal(mount.Definition)
		if err != nil {
			return fmt.Errorf("fingerprint tool definition %s: %w", mount.Definition.Name, err)
		}
		desired[mount.Definition.Name] = sha256.Sum256(encoded)
	}

	var stale []string
	for name := range s.mountedDownstreamTools {
		if _, keep := desired[name]; !keep {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		// Remove before adding so an interleaved tools/list may temporarily
		// under-advertise but can never observe a union that violates scope or
		// the schema budget.
		s.srv.RemoveTools(stale...)
	}
	for _, mount := range mounts {
		fingerprint := desired[mount.Definition.Name]
		if previous, unchanged := s.mountedDownstreamTools[mount.Definition.Name]; unchanged && previous == fingerprint {
			continue
		}
		// SDK AddTool replaces by name and emits a debounced list_changed
		// notification. The handler routes through Hub.Call by server name, so
		// an unchanged definition needs no replacement after a reconnect.
		s.srv.AddTool(mount.Definition, mount.Handler)
	}

	s.mountedDownstreamTools = desired
	s.toolMountReport = report
	s.advertisedDownstreamTools = len(mounts)
	return nil
}

func (s *Server) watchDownstreamTools(ctx context.Context, changes <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-changes:
			if err := s.mountDownstreamTools(); err != nil {
				// stdio JSON-RPC owns stdout. Keep this fallback bounded and
				// fixed as the error may contain downstream-controlled names.
				fmt.Fprintln(os.Stderr, "mcphub: downstream tool refresh failed")
			}
		}
	}
}

func (s *Server) mountDiagnostics() (int, *hub.ToolMountReport) {
	s.mountMu.RLock()
	defer s.mountMu.RUnlock()
	var report *hub.ToolMountReport
	if s.toolMountReport != nil {
		copy := *s.toolMountReport
		report = &copy
	}
	return s.advertisedDownstreamTools, report
}

func (s *Server) registerManagement() {
	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "mcphub_list_servers",
		Description: "List configured downstream servers with enabled/connected state and tool counts.",
	}, s.handleListServers)

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "mcphub_search_tools",
		Description: "Search and rank hidden downstream tools from natural-language intent. Matches tool metadata plus server descriptions, tags, and use_when routing hints; returns `server__tool` names for mcphub_call_tool.",
	}, s.handleSearchTools)

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "mcphub_describe_tool",
		Description: "Return a downstream tool's description and full JSON input schema, so you can construct a valid mcphub_call_tool request.",
	}, s.handleDescribeTool)

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "mcphub_resolve_tool",
		Description: "Contextual capability router. Call proactively when a task starts or changes phase: describe the current goal/activity in natural language and receive the best hidden tool, why it matched, required fields, an argument template, and ranked alternatives.",
	}, s.handleResolveTool)

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "mcphub_call_tool",
		Description: "Invoke a downstream tool by name. Oversized results return a lossless retrieval receipt for mcphub_get_result; small results pass through unchanged. Accepts {server, tool, arguments} (tool may be the combined `server__tool` form). This is how you call tools in lazy mode. For long-running tools (repository indexing, large scans, batch jobs) that could exceed your client's tool-call timeout, pass detach: true — the call keeps running in the background and you get a callId immediately; collect the outcome with mcphub_poll_result. An optional timeout_ms bounds the call (clamped by the gateway's call_timeout config).",
	}, s.handleCallTool)

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "mcphub_get_result",
		Description: "Retrieve a bounded base64 page of a complete result previously stored by mcphub. Start with cursor 0 and continue with nextCursor until done is true.",
	}, s.handleGetResult)

	sdk.AddTool(s.srv, &sdk.Tool{
		Name:        "mcphub_poll_result",
		Description: "Check on a detached (detach: true) mcphub_call_tool invocation by callId. Returns {status: pending} while the downstream call is still running (poll again after a delay), {status: failed} with the error if it failed, or — once complete — the tool's result itself, exactly as a synchronous call would have returned it (an oversized result appears as a stored-result receipt for mcphub_get_result). Completed results are retained for 24 hours; detached calls do not survive a gateway restart, in which case the callId reports status: unknown.",
	}, s.handlePollResult)

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
	Query   string `json:"query" jsonschema:"natural-language capability or task context"`
	MaxHits int    `json:"max_hits,omitempty" jsonschema:"maximum matches to return (default 20, max 100)"`
}

type describeInput struct {
	Server string `json:"server,omitempty" jsonschema:"downstream server name (optional if tool is server__tool)"`
	Tool   string `json:"tool" jsonschema:"tool name; may be the combined server__tool form. A server's own name prefix may be collapsed: hitspec__search_web resolves to hitspec__hitspec_search_web"`
}

type callInput struct {
	Server    string         `json:"server,omitempty" jsonschema:"downstream server name (optional if tool is server__tool)"`
	Tool      string         `json:"tool" jsonschema:"tool name; may be the combined server__tool form. A server's own name prefix may be collapsed: hitspec__search_web resolves to hitspec__hitspec_search_web"`
	Arguments map[string]any `json:"arguments,omitempty" jsonschema:"arguments object passed to the downstream tool"`
	Detach    bool           `json:"detach,omitempty" jsonschema:"run the call in the background and return a callId immediately; collect the result with mcphub_poll_result. Use for long-running tools that could exceed the client tool-call timeout"`
	TimeoutMs int64          `json:"timeout_ms,omitempty" jsonschema:"optional per-call timeout in milliseconds, clamped by the gateway's call_timeout config (default 30m). Bounds a detached call's background execution; on a synchronous call it can only shorten the effective deadline"`
}

type getResultInput struct {
	CallID string `json:"callId" jsonschema:"opaque call ID from a stored-result receipt"`
	Cursor int64  `json:"cursor,omitempty" jsonschema:"zero-based byte cursor (default 0)"`
}

type pollResultInput struct {
	CallID string `json:"callId" jsonschema:"opaque call ID returned by a detached (detach: true) mcphub_call_tool invocation"`
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
	UseWhen     []string `json:"use_when,omitempty"`
	Error       string   `json:"error,omitempty"`
}

func (s *Server) handleListServers(_ context.Context, _ *sdk.CallToolRequest, _ emptyInput) (*sdk.CallToolResult, any, error) {
	state := map[string]*hub.Downstream{}
	for _, d := range s.hub.Downstreams() {
		state[d.Name] = d
	}
	totalTools := 0
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
			UseWhen:     srv.UseWhen,
		}
		if d, ok := state[name]; ok {
			info.Connected = d.Connected()
			for _, tool := range d.ToolsSnapshot() {
				if s.scope.allows(name, tool.Name) {
					info.Tools++
				}
			}
			if info.Connected {
				totalTools += info.Tools
			}
			if err := d.ErrorSnapshot(); err != nil {
				info.Error = err.Error()
			}
		}
		out = append(out, info)
	}
	expose := config.ExposeAll
	if s.cfg.Lazy() {
		expose = config.ExposeLazy
	}
	catalog := s.catalog()
	advertisedDownstreamTools, toolMountReport := s.mountDiagnostics()
	response := map[string]any{
		"contract_version":            contextualRoutingContractVersion,
		"catalog_revision":            catalogRevision(catalog),
		"servers":                     out,
		"total_tools":                 totalTools,
		"expose":                      expose,
		"pinned":                      s.scope.effectivePins(s.cfg),
		"management_tools":            managementToolCount,
		"advertised_downstream_tools": advertisedDownstreamTools,
	}
	if toolMountReport != nil {
		response["tool_schema_budget"] = *toolMountReport
	}
	return result(response)
}

func (s *Server) handleSearchTools(_ context.Context, _ *sdk.CallToolRequest, in searchInput) (*sdk.CallToolResult, any, error) {
	if message := discoveryQueryError(in.Query); message != "" {
		return result(map[string]any{"error": message, "max_query_bytes": maxDiscoveryQueryBytes})
	}
	catalog := s.catalog()
	matches := rankCatalog(in.Query, catalog)
	count := len(matches)
	maxHits := in.MaxHits
	if maxHits <= 0 {
		maxHits = 20
	}
	if maxHits > 100 {
		maxHits = 100
	}
	returned, byteLimited := compactToolMatches(matches, maxHits)
	return result(map[string]any{
		"contract_version": contextualRoutingContractVersion,
		"catalog_revision": catalogRevision(catalog),
		"query":            in.Query,
		"count":            count,
		"returned":         len(returned),
		"truncated":        len(returned) < count,
		"byte_limited":     byteLimited,
		"matches":          returned,
	})
}

func (s *Server) handleDescribeTool(_ context.Context, _ *sdk.CallToolRequest, in describeInput) (*sdk.CallToolResult, any, error) {
	server, tool := splitNamespaced(in.Server, in.Tool)
	if server == "" || tool == "" {
		return result(map[string]any{"error": "need server and tool (or a server__tool name)"})
	}
	// Adopt the canonical downstream name before the scope check so the
	// stutter-collapsed alias (hitspec__search_web for hitspec's own
	// hitspec_search_web) is scoped and described as the tool it names.
	canonical, t, found := s.hub.CanonicalTool(server, tool)
	if found {
		tool = canonical
	}
	if !s.scope.allows(server, tool) {
		return nil, nil, fmt.Errorf("tool %s__%s is out of scope for this agent", server, tool)
	}
	if !found {
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
	if message := discoveryQueryError(in.Query); message != "" {
		return result(map[string]any{"error": message, "max_query_bytes": maxDiscoveryQueryBytes})
	}
	catalog := s.catalog()
	revision := catalogRevision(catalog)
	if len(intentTerms(in.Query)) == 0 {
		return result(map[string]any{
			"contract_version": contextualRoutingContractVersion,
			"catalog_revision": revision,
			"status":           "no_match",
			"confidence":       "none",
			"reason_codes":     []string{"query_too_generic"},
			"query":            in.Query,
			"recommendation":   nil,
			"ambiguous":        false,
			"alternatives":     []toolMatch{},
			"hint":             "query must describe the current goal, activity, or capability",
		})
	}
	maxHits := in.MaxHits
	if maxHits <= 0 || maxHits > 10 {
		maxHits = 5
	}
	matches := rankCatalog(in.Query, catalog)
	assessment := assessRoute(in.Query, matches)
	if len(matches) == 0 {
		return result(map[string]any{
			"contract_version": contextualRoutingContractVersion,
			"catalog_revision": revision,
			"status":           assessment.Status,
			"confidence":       assessment.Confidence,
			"reason_codes":     assessment.ReasonCodes,
			"query":            in.Query,
			"recommendation":   nil,
			"ambiguous":        false,
			"alternatives":     []toolMatch{},
			"hint":             "no tools matched — describe the capability with different terms, add use_when hints to the server, or browse with mcphub_search_tools",
		})
	}
	top := matches[0]
	alts := matches[1:]
	compactTop := compactToolMatch(top)
	compactAlts, alternativesByteLimited := compactToolMatches(alts, maxHits)
	ambiguous := assessment.Status == "ambiguous"
	// Extract a bounded top-level argument summary. Full schemas remain available
	// through mcphub_describe_tool when fields or encoded input are omitted.
	t, ok := s.hub.FindTool(top.Server, top.Tool)
	required, template, templateTruncated := []string{}, map[string]any{}, false
	if ok && t.InputSchema != nil {
		required, template, templateTruncated = summarizeInputSchema(t.InputSchema)
	}
	return result(map[string]any{
		"contract_version": contextualRoutingContractVersion,
		"catalog_revision": revision,
		"status":           assessment.Status,
		"confidence":       assessment.Confidence,
		"reason_codes":     assessment.ReasonCodes,
		"matched_fraction": assessment.MatchedFraction,
		"score_gap":        assessment.ScoreGap,
		"query":            in.Query,
		"recommendation": map[string]any{
			"server":                      compactTop.Server,
			"tool":                        compactTop.Tool,
			"namespaced":                  compactTop.Namespaced,
			"title":                       compactTop.Title,
			"description":                 compactTop.Description,
			"server_description":          compactTop.ServerDescription,
			"use_when":                    compactTop.UseWhen,
			"tool_use_when":               compactTop.ToolUseWhen,
			"score":                       compactTop.Score,
			"matched_terms":               compactTop.MatchedTerms,
			"metadata_truncated":          compactTop.MetadataTruncated,
			"required_fields":             required,
			"argument_template":           template,
			"argument_template_truncated": templateTruncated,
		},
		"ambiguous":              ambiguous,
		"alternatives":           compactAlts,
		"alternatives_truncated": alternativesByteLimited || len(compactAlts) < len(alts),
		"hint":                   resolveHint(ambiguous, top.Namespaced, templateTruncated),
	})
}

const (
	maxResolveTemplateFields     = 48
	maxResolveFieldNameBytes     = 128
	maxResolveFieldNamesBytes    = 2048
	maxResolveSchemaInspectBytes = 256 * 1024
)

func summarizeInputSchema(input any) ([]string, map[string]any, bool) {
	required := []string{}
	template := map[string]any{}
	schema, ok := inputSchemaMap(input)
	if !ok {
		return required, template, true
	}
	usedBytes := 0
	truncated := false
	add := func(name string, requiredField bool) {
		if _, exists := template[name]; exists {
			return
		}
		if name == "" || len(name) > maxResolveFieldNameBytes || len(template) == maxResolveTemplateFields || usedBytes+len(name) > maxResolveFieldNamesBytes {
			truncated = true
			return
		}
		usedBytes += len(name)
		if requiredField {
			required = append(required, name)
			template[name] = "<value>"
			return
		}
		template[name] = nil
	}
	addRequired := func(fields []any) {
		limit := len(fields)
		if limit > maxResolveTemplateFields {
			limit = maxResolveTemplateFields
			truncated = true
		}
		for _, field := range fields[:limit] {
			name, ok := field.(string)
			if !ok {
				truncated = true
				continue
			}
			add(name, true)
		}
	}
	switch fields := schema["required"].(type) {
	case []any:
		addRequired(fields)
	case []string:
		limit := len(fields)
		if limit > maxResolveTemplateFields {
			limit = maxResolveTemplateFields
			truncated = true
		}
		for _, name := range fields[:limit] {
			add(name, true)
		}
	case nil:
	default:
		truncated = true
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		if len(properties) > maxResolveTemplateFields {
			return required, template, true
		}
		names := make([]string, 0, len(properties))
		for name := range properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			add(name, false)
		}
	}
	return required, template, truncated
}

// inputSchemaMap accepts the representation returned by MCP clients directly.
// Raw JSON is decoded only under a fixed byte limit; arbitrary Go values are
// deliberately not marshaled because doing so would traverse unbounded data.
func inputSchemaMap(input any) (map[string]any, bool) {
	switch schema := input.(type) {
	case map[string]any:
		return schema, true
	case json.RawMessage:
		return decodeInputSchema(schema)
	case []byte:
		return decodeInputSchema(schema)
	default:
		return nil, false
	}
}

func decodeInputSchema(encoded []byte) (map[string]any, bool) {
	if len(encoded) == 0 || len(encoded) > maxResolveSchemaInspectBytes {
		return nil, false
	}
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		return nil, false
	}
	return schema, true
}

func resolveHint(ambiguous bool, namespaced string, templateTruncated bool) string {
	if ambiguous {
		return "recommendation is ambiguous — do not auto-run it; compare alternatives, search with more specific intent, or ask for direction, then use mcphub_describe_tool for the complete schema"
	}
	if templateTruncated {
		return fmt.Sprintf("argument template was bounded — call mcphub_describe_tool for %s before invoking it", namespaced)
	}
	return "call mcphub_call_tool with server + tool (or the namespaced name) + the argument_template filled in"
}

func (s *Server) handleCallTool(ctx context.Context, _ *sdk.CallToolRequest, in callInput) (*sdk.CallToolResult, any, error) {
	server, tool := splitNamespaced(in.Server, in.Tool)
	if server == "" || tool == "" {
		return nil, nil, fmt.Errorf("need server and tool (or a server__tool name)")
	}
	// Adopt the canonical downstream name before the scope check so the
	// stutter-collapsed alias (hitspec__search_web for hitspec's own
	// hitspec_search_web) is scoped, called, and recorded as the tool it
	// names. An unresolvable name passes through — Call/StartDetached report
	// unknown-server/not-found with their existing errors.
	if canonical, _, ok := s.hub.CanonicalTool(server, tool); ok {
		tool = canonical
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
	timeout := s.effectiveCallTimeout(in.TimeoutMs)
	if in.Detach {
		callID, err := s.hub.StartDetached(ctx, server, tool, args, timeout)
		if err != nil {
			return nil, nil, fmt.Errorf("detach %s__%s: %w", server, tool, err)
		}
		return result(map[string]any{
			"status":     "accepted",
			"callId":     callID,
			"server":     server,
			"tool":       tool,
			"namespaced": server + "__" + tool,
			"timeoutMs":  timeout.Milliseconds(),
			"nextAction": "The call is running in the background. Call mcphub_poll_result with this callId; status pending means poll again after a delay, and a completed call returns the tool result itself (or a stored-result receipt for mcphub_get_result if it is oversized).",
		})
	}
	if in.TimeoutMs > 0 {
		// Synchronous path: an explicit timeout_ms can only tighten the deadline
		// (the client's own request deadline still applies via ctx).
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	res, err := s.hub.Call(ctx, server, tool, args)
	if err != nil {
		return nil, nil, fmt.Errorf("call %s__%s: %w", server, tool, err)
	}
	return res, nil, nil
}

// effectiveCallTimeout resolves the per-call ceiling: the caller's timeout_ms
// when given, clamped to the configured call_timeout (default 30m).
func (s *Server) effectiveCallTimeout(timeoutMs int64) time.Duration {
	limit := 30 * time.Minute
	if s.cfg != nil {
		limit = s.cfg.CallTimeoutDuration()
	}
	// Compare in milliseconds before converting to time.Duration: a huge
	// timeout_ms would overflow Duration's int64 nanoseconds and slip past the
	// clamp as a negative duration — an immediately-expired deadline that would
	// fail the call instantly and needlessly invalidate the downstream session.
	if timeoutMs <= 0 || timeoutMs > limit.Milliseconds() {
		return limit
	}
	return time.Duration(timeoutMs) * time.Millisecond
}

func (s *Server) handlePollResult(_ context.Context, _ *sdk.CallToolRequest, in pollResultInput) (*sdk.CallToolResult, any, error) {
	if strings.TrimSpace(in.CallID) == "" {
		return nil, nil, fmt.Errorf("callId is required")
	}
	call, ok := s.hub.PollDetached(in.CallID)
	if !ok {
		return result(map[string]any{
			"status": "unknown",
			"callId": in.CallID,
			"reason": "The callId is unknown: it may have expired, been evicted, or the gateway restarted (detached calls do not survive restarts). Re-run the call with detach: true, or use mcphub_get_result if this callId came from a stored-result receipt.",
		})
	}
	if !s.scope.allows(call.Server, call.Tool) {
		return nil, nil, fmt.Errorf("detached call for %s is out of scope for this agent", call.Namespaced)
	}
	switch call.Status {
	case hub.DetachedPending:
		return result(map[string]any{
			"status":     "pending",
			"callId":     call.ID,
			"namespaced": call.Namespaced,
			"elapsedMs":  time.Since(call.StartedAt).Milliseconds(),
			"hint":       "still running — poll again after a delay",
		})
	case hub.DetachedFailed:
		return result(map[string]any{
			"status":     "failed",
			"callId":     call.ID,
			"namespaced": call.Namespaced,
			"error":      call.Err,
			"elapsedMs":  call.CompletedAt.Sub(call.StartedAt).Milliseconds(),
		})
	}
	// Done: hand back the finalized result exactly as a synchronous call would
	// have returned it (oversized results are already spooled receipts).
	return call.Result, nil, nil
}

const maxResultPageSize int64 = 8 * 1024

func (s *Server) resultPageSize() int64 {
	if s.cfg == nil {
		return maxResultPageSize
	}
	budget := s.cfg.ResponseBudgetBytes()
	if budget <= 0 {
		return maxResultPageSize
	}
	// Data is base64-encoded into both text and structured MCP content. Reserve
	// the observed fixed envelope plus a conservative 3x expansion factor; this
	// keeps even the minimum valid 512-byte budget bounded.
	const envelopeHeadroom = 448
	available := budget - envelopeHeadroom
	if available < 1 {
		return 1
	}
	size := int64(available / 3)
	if size > maxResultPageSize {
		return maxResultPageSize
	}
	return size
}

func (s *Server) handleGetResult(ctx context.Context, _ *sdk.CallToolRequest, in getResultInput) (*sdk.CallToolResult, any, error) {
	if strings.TrimSpace(in.CallID) == "" {
		return nil, nil, fmt.Errorf("callId is required")
	}
	if in.Cursor < 0 {
		return nil, nil, fmt.Errorf("cursor must be nonnegative")
	}
	if s.store == nil {
		return nil, nil, fmt.Errorf("result store not configured")
	}
	page, err := s.store.ReadResultPage(ctx, in.CallID, in.Cursor, s.resultPageSize())
	switch {
	case errors.Is(err, store.ErrResultNotFound), errors.Is(err, store.ErrResultExpired):
		return result(map[string]any{
			"status": "unavailable",
			"reason": "The callId is unknown or its stored result has expired.",
			"callId": in.CallID,
		})
	case err != nil && !errors.Is(err, store.ErrResultCursorOutOfRange):
		return nil, nil, fmt.Errorf("read stored result: %w", err)
	}
	if !s.scope.allows(page.Server, page.Tool) {
		return nil, nil, fmt.Errorf("stored result for %s__%s is out of scope for this agent", page.Server, page.Tool)
	}
	if errors.Is(err, store.ErrResultCursorOutOfRange) {
		return result(map[string]any{
			"status": "cursor_out_of_range",
			"reason": "cursor is beyond the end of the stored result",
			"callId": in.CallID,
			"cursor": in.Cursor,
		})
	}
	return result(map[string]any{
		"status":     "ok",
		"callId":     in.CallID,
		"mediaType":  "application/json",
		"data":       base64.StdEncoding.EncodeToString(page.Data),
		"cursor":     page.Cursor,
		"nextCursor": page.NextCursor,
		"done":       page.Done,
		"totalBytes": page.TotalBytes,
	})
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
