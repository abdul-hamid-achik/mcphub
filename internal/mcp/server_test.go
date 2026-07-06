package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/hub"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
)

func TestSplitNamespaced(t *testing.T) {
	cases := []struct {
		name                 string
		inServer, inTool     string
		wantServer, wantTool string
	}{
		{"combined", "", "srv__tool", "srv", "tool"},
		{"explicit unchanged", "srv", "tool", "srv", "tool"},
		{"combined first split only", "", "srv__a__b", "srv", "a__b"},
		{"explicit not resplit", "srv", "a__b", "srv", "a__b"},
		{"no separator", "", "noseparator", "", "noseparator"},
		// agent echoes the namespaced form into tool while also setting server.
		{"redundant prefix stripped", "codemap", "codemap__codemap_find", "codemap", "codemap_find"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotS, gotT := splitNamespaced(c.inServer, c.inTool)
			if gotS != c.wantServer || gotT != c.wantTool {
				t.Fatalf("splitNamespaced(%q,%q) = (%q,%q), want (%q,%q)",
					c.inServer, c.inTool, gotS, gotT, c.wantServer, c.wantTool)
			}
		})
	}
}

// testServer builds a Server with a hub (no downstreams connected) and an
// in-memory store, suitable for testing the meta-tool handlers.
func testServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{
		Servers: map[string]config.Server{
			"codemap": {Command: "codemap", Enabled: true, Description: "Code knowledge graph"},
			"monitor": {Command: "monitor", Enabled: false, Description: "System observability"},
		},
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := hub.New(cfg, st, nil)
	return &Server{srv: nil, hub: h, store: st, cfg: cfg}
}

func TestHandleListServers(t *testing.T) {
	s := testServer(t)
	res, _, err := s.handleListServers(context.Background(), nil, emptyInput{})
	if err != nil {
		t.Fatal(err)
	}
	body := textContent(res)
	for _, want := range []string{`"name": "codemap"`, `"enabled": true`, `"name": "monitor"`, `"enabled": false`, `"connected": false`} {
		if !strings.Contains(body, want) {
			t.Errorf("list output missing %q:\n%s", want, body)
		}
	}
}

func TestHandleSearchToolsEmpty(t *testing.T) {
	s := testServer(t)
	// No downstreams are connected, so even an empty query returns no matches.
	res, _, err := s.handleSearchTools(context.Background(), nil, searchInput{Query: ""})
	if err != nil {
		t.Fatal(err)
	}
	body := textContent(res)
	if !strings.Contains(body, `"count": 0`) {
		t.Errorf("expected count 0 with no connected downstreams:\n%s", body)
	}
}

func TestHandleDescribeToolNotFound(t *testing.T) {
	s := testServer(t)
	res, _, err := s.handleDescribeTool(context.Background(), nil, describeInput{Tool: "ghost__nope"})
	if err != nil {
		t.Fatal(err)
	}
	body := textContent(res)
	if !strings.Contains(body, "not found") {
		t.Errorf("expected 'not found' error:\n%s", body)
	}
}

func TestHandleDescribeToolEmptyInput(t *testing.T) {
	s := testServer(t)
	res, _, err := s.handleDescribeTool(context.Background(), nil, describeInput{})
	if err != nil {
		t.Fatal(err)
	}
	body := textContent(res)
	if !strings.Contains(body, "need server and tool") {
		t.Errorf("expected 'need server and tool' error:\n%s", body)
	}
}

func TestHandleCallToolEmptyInput(t *testing.T) {
	s := testServer(t)
	_, _, err := s.handleCallTool(context.Background(), nil, callInput{})
	if err == nil {
		t.Fatal("expected error for empty server/tool, got nil")
	}
	if !strings.Contains(err.Error(), "need server and tool") {
		t.Errorf("expected 'need server and tool' error, got %v", err)
	}
}

func TestHandleCallToolUnknownServer(t *testing.T) {
	s := testServer(t)
	_, _, err := s.handleCallTool(context.Background(), nil, callInput{Tool: "ghost__tool"})
	if err == nil {
		t.Fatal("expected error for unknown server, got nil")
	}
	if !strings.Contains(err.Error(), "unknown server") {
		t.Errorf("expected 'unknown server' error, got %v", err)
	}
}

func TestHandleStats(t *testing.T) {
	s := testServer(t)
	// Record a call so stats has something to show.
	ctx := context.Background()
	if err := s.store.RecordCall(ctx, store.CallRecord{Server: "codemap", Tool: "search", Namespaced: "codemap__search", Duration: 1000000, ArgsBytes: 10, ResultBytes: 20}); err != nil {
		t.Fatal(err)
	}
	res, _, err := s.handleStats(ctx, nil, emptyInput{})
	if err != nil {
		t.Fatal(err)
	}
	body := textContent(res)
	if !strings.Contains(body, `"calls": 1`) {
		t.Errorf("expected calls=1 in stats:\n%s", body)
	}
}

// textContent extracts the text from the first TextContent block of a result.
func textContent(res *sdk.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*sdk.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestAgentScopeAllows(t *testing.T) {
	var nilScope *agentScope
	if !nilScope.allowsServer("a") || !nilScope.allows("a", "x") || !nilScope.allowsNS("a__x") {
		t.Error("nil scope should allow everything")
	}

	serversOnly := &agentScope{servers: map[string]bool{"a": true, "b": true}}
	if !serversOnly.allowsServer("a") || serversOnly.allowsServer("c") {
		t.Error("servers-only scope: allowsServer wrong")
	}
	if !serversOnly.allows("a", "x") || serversOnly.allows("c", "x") {
		t.Error("servers-only scope: allows wrong (c not in servers)")
	}
	// No tool set => all tools of allowed servers are allowed.
	if !serversOnly.allows("b", "anything") {
		t.Error("servers-only scope should allow any tool of an allowed server")
	}

	scoped := &agentScope{servers: map[string]bool{"a": true}, tools: map[string]bool{"a__x": true}}
	if !scoped.allows("a", "x") {
		t.Error("scoped: a__x should be allowed")
	}
	if scoped.allows("a", "y") {
		t.Error("scoped: a__y should be denied (not in tool set)")
	}
	if scoped.allows("b", "x") {
		t.Error("scoped: b__x should be denied (server not allowed)")
	}
	if scoped.allowsNS("a__y") || !scoped.allowsNS("a__x") {
		t.Error("scoped: allowsNS wrong")
	}
	if scoped.allowsNS("nope") {
		t.Error("scoped: allowsNS should reject a name without __")
	}
}
func TestScopeFor(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]config.Server{"a": {Command: "a", Enabled: true}, "b": {Command: "b", Enabled: true}},
		Agents: map[string]config.Agent{
			"plain":   {Type: "claude", Path: "~/p"},
			"scoped":  {Type: "claude", Path: "~/p", Servers: &[]string{"a"}, Tools: &[]string{"a__x"}},
			"srvOnly": {Type: "claude", Path: "~/p", Servers: &[]string{"b"}},
		},
	}
	if sc, err := ScopeFor(cfg, ""); err != nil || sc != nil {
		t.Errorf("empty agent name => nil scope, got %v %v", sc, err)
	}
	if sc, err := ScopeFor(cfg, "plain"); err != nil || sc != nil {
		t.Errorf("agent with no routing => nil scope, got %v %v", sc, err)
	}
	if _, err := ScopeFor(cfg, "ghost"); err == nil {
		t.Error("unknown agent name should error")
	}
	sc, err := ScopeFor(cfg, "scoped")
	if err != nil || sc == nil || !sc.servers["a"] || sc.servers["b"] || !sc.tools["a__x"] {
		t.Errorf("scoped agent scope = %+v err=%v", sc, err)
	}
	sc2, err := ScopeFor(cfg, "srvOnly")
	if err != nil || sc2 == nil || !sc2.servers["b"] || sc2.tools != nil {
		t.Errorf("srvOnly scope = %+v err=%v (tools should be nil)", sc2, err)
	}
}

// testScopedServer builds a Server carrying an agentScope (servers=a, tools=a__x)
// so the meta-tool handlers can be exercised against a scoped gateway.
func testScopedServer(t *testing.T) *Server {
	t.Helper()
	s := testServer(t)
	s.scope = &agentScope{servers: map[string]bool{"codemap": true}, tools: map[string]bool{"codemap__search": true}}
	return s
}

func TestHandleListServersScoped(t *testing.T) {
	s := testScopedServer(t)
	res, _, err := s.handleListServers(context.Background(), nil, emptyInput{})
	if err != nil {
		t.Fatal(err)
	}
	body := textContent(res)
	if !strings.Contains(body, `"name": "codemap"`) {
		t.Errorf("scoped list should include codemap:\n%s", body)
	}
	if strings.Contains(body, `"name": "monitor"`) {
		t.Errorf("scoped list should exclude monitor (out of scope):\n%s", body)
	}
}

func TestHandleDescribeToolOutOfScope(t *testing.T) {
	s := testScopedServer(t)
	// codemap is allowed but codemap__other is not in the tool set.
	_, _, err := s.handleDescribeTool(context.Background(), nil, describeInput{Tool: "codemap__other"})
	if err == nil || !strings.Contains(err.Error(), "out of scope") {
		t.Errorf("expected out-of-scope error, got %v", err)
	}
	// codemap__search is in scope; it won't be found (no downstream connected)
	// but that's a 'not found', not an out-of-scope rejection.
	res, _, err := s.handleDescribeTool(context.Background(), nil, describeInput{Tool: "codemap__search"})
	if err != nil {
		t.Fatalf("in-scope describe returned error: %v", err)
	}
	if !strings.Contains(textContent(res), "not found") {
		t.Errorf("in-scope but unconnected tool should report 'not found':\n%s", textContent(res))
	}
}

func TestHandleCallToolOutOfScope(t *testing.T) {
	s := testScopedServer(t)
	_, _, err := s.handleCallTool(context.Background(), nil, callInput{Tool: "codemap__other"})
	if err == nil || !strings.Contains(err.Error(), "out of scope") {
		t.Errorf("expected out-of-scope error, got %v", err)
	}
}
