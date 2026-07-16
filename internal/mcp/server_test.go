package mcp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestHandleResolveToolRequiresContext(t *testing.T) {
	s := testServer(t)
	res, out, err := s.handleResolveTool(context.Background(), nil, resolveToolInput{Query: "  the  "})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(textContent(res), "query must describe") {
		t.Fatalf("unexpected result: %s", textContent(res))
	}
	if out.(map[string]any)["recommendation"] != nil {
		t.Fatalf("blank context recommendation = %#v", out)
	}
}

func TestSummarizeInputSchemaIsBoundedAndDeterministic(t *testing.T) {
	required := make([]any, 0, maxResolveTemplateFields+20)
	properties := map[string]any{}
	for i := 0; i < maxResolveTemplateFields+20; i++ {
		name := fmt.Sprintf("field_%03d", i)
		required = append(required, name)
		properties[name] = map[string]any{"type": "string"}
	}
	gotRequired, template, truncated := summarizeInputSchema(map[string]any{
		"type": "object", "required": required, "properties": properties,
	})
	if !truncated || len(gotRequired) != maxResolveTemplateFields || len(template) != maxResolveTemplateFields {
		t.Fatalf("schema summary required=%d template=%d truncated=%t", len(gotRequired), len(template), truncated)
	}
	if gotRequired[0] != "field_000" || gotRequired[len(gotRequired)-1] != "field_047" {
		t.Fatalf("required field order changed: %v", gotRequired)
	}
	nameBytes := 0
	for name := range template {
		nameBytes += len(name)
	}
	if nameBytes > maxResolveFieldNamesBytes {
		t.Fatalf("template field names use %d bytes, max %d", nameBytes, maxResolveFieldNamesBytes)
	}

	_, hugeTemplate, hugeTruncated := summarizeInputSchema(json.RawMessage(strings.Repeat(" ", maxResolveSchemaInspectBytes+1)))
	if !hugeTruncated || len(hugeTemplate) != 0 {
		t.Fatalf("oversized schema summary = %#v truncated=%t", hugeTemplate, hugeTruncated)
	}

	_, unknownTemplate, unknownTruncated := summarizeInputSchema(struct {
		Payload string `json:"payload"`
	}{Payload: strings.Repeat("x", maxResolveSchemaInspectBytes+1)})
	if !unknownTruncated || len(unknownTemplate) != 0 {
		t.Fatalf("arbitrary schema summary = %#v truncated=%t", unknownTemplate, unknownTruncated)
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

func serverWithResultStore(t *testing.T, scope *agentScope) (*Server, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "results.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := &config.Config{Expose: config.ExposeLazy}
	h := hub.New(cfg, st, nil)
	return NewServer(cfg, h, st, scope), path
}

func connectServerClient(t *testing.T, server *sdk.Server) *sdk.ClientSession {
	t.Helper()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "mcp-test", Version: "1"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	})
	return clientSession
}

func TestGetResultRegistrationAndWireReconstruction(t *testing.T) {
	s, _ := serverWithResultStore(t, nil)
	payload := append([]byte(`{"content":[{"type":"text","text":"`), bytes.Repeat([]byte("wire-data-"), 2500)...)
	payload = append(payload, []byte(`"}],"isError":false}`)...)
	callID, err := s.store.PutResult(context.Background(), "memory", "large", payload)
	if err != nil {
		t.Fatal(err)
	}

	client := connectServerClient(t, s.srv)
	tools, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var registered *sdk.Tool
	for _, tool := range tools.Tools {
		if tool.Name == "mcphub_get_result" {
			registered = tool
			break
		}
	}
	if registered == nil {
		t.Fatal("mcphub_get_result is not always registered")
	}
	schema, err := json.Marshal(registered.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(schema, []byte(`"required":["callId"]`)) {
		t.Fatalf("callId is not required in wire schema: %s", schema)
	}

	var rebuilt []byte
	var cursor int64
	pages := 0
	for {
		res, err := client.CallTool(context.Background(), &sdk.CallToolParams{
			Name:      "mcphub_get_result",
			Arguments: json.RawMessage(fmt.Sprintf(`{"callId":%q,"cursor":%d}`, callID, cursor)),
		})
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		if len(envelope) >= 32*1024 {
			t.Fatalf("result page envelope = %d bytes; page size left no default-budget headroom", len(envelope))
		}
		out, ok := res.StructuredContent.(map[string]any)
		if !ok {
			t.Fatalf("wire structured output type = %T", res.StructuredContent)
		}
		if out["status"] != "ok" || out["mediaType"] != "application/json" {
			t.Fatalf("page output = %#v", out)
		}
		data, err := base64.StdEncoding.DecodeString(out["data"].(string))
		if err != nil {
			t.Fatal(err)
		}
		rebuilt = append(rebuilt, data...)
		cursor = int64(out["nextCursor"].(float64))
		pages++
		if out["done"].(bool) {
			if got := int64(out["totalBytes"].(float64)); got != int64(len(payload)) {
				t.Fatalf("totalBytes = %d, want %d", got, len(payload))
			}
			break
		}
	}
	if pages < 2 {
		t.Fatalf("large result used %d page; want multiple bounded pages", pages)
	}
	if !bytes.Equal(rebuilt, payload) {
		t.Fatal("mcphub_get_result wire pages did not reconstruct exact bytes")
	}
}

func TestGetResultPageRespectsConfiguredBudget(t *testing.T) {
	s, _ := serverWithResultStore(t, nil)
	s.cfg.ResponseBudget = "900B"
	payload := bytes.Repeat([]byte("bounded-page-data-"), 500)
	callID, err := s.store.PutResult(context.Background(), "memory", "large", payload)
	if err != nil {
		t.Fatal(err)
	}

	client := connectServerClient(t, s.srv)
	res, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "mcphub_get_result",
		Arguments: json.RawMessage(fmt.Sprintf(`{"callId":%q,"cursor":0}`, callID)),
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope) >= 900 {
		t.Fatalf("result page envelope = %d bytes, want below configured 900-byte budget", len(envelope))
	}
	out := res.StructuredContent.(map[string]any)
	data, err := base64.StdEncoding.DecodeString(out["data"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := int64(len(data)), s.resultPageSize(); got != want {
		t.Fatalf("page bytes = %d, want %d", got, want)
	}
}

func TestGetResultPageFitsMinimumConfiguredBudget(t *testing.T) {
	s, _ := serverWithResultStore(t, nil)
	s.cfg.ResponseBudget = "512B"
	callID, err := s.store.PutResult(context.Background(), "memory", "large", bytes.Repeat([]byte("x"), 4096))
	if err != nil {
		t.Fatal(err)
	}
	client := connectServerClient(t, s.srv)
	res, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "mcphub_get_result",
		Arguments: json.RawMessage(fmt.Sprintf(`{"callId":%q,"cursor":0}`, callID)),
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope) > config.MinResponseBudgetBytes {
		t.Fatalf("minimum-budget result page envelope = %d bytes, budget = %d", len(envelope), config.MinResponseBudgetBytes)
	}
}

func TestHandleGetResultValidationScopeAndOutcomes(t *testing.T) {
	scope := &agentScope{
		servers: map[string]bool{"codemap": true},
		tools:   map[string]bool{"codemap__search": true},
	}
	s, path := serverWithResultStore(t, scope)
	ctx := context.Background()
	if _, _, err := s.handleGetResult(ctx, nil, getResultInput{}); err == nil || !strings.Contains(err.Error(), "callId") {
		t.Fatalf("missing callId error = %v", err)
	}
	if _, _, err := s.handleGetResult(ctx, nil, getResultInput{CallID: "x", Cursor: -1}); err == nil || !strings.Contains(err.Error(), "nonnegative") {
		t.Fatalf("negative cursor error = %v", err)
	}

	allowedID, err := s.store.PutResult(ctx, "codemap", "search", []byte("allowed"))
	if err != nil {
		t.Fatal(err)
	}
	if _, out, err := s.handleGetResult(ctx, nil, getResultInput{CallID: allowedID}); err != nil || out.(map[string]any)["status"] != "ok" {
		t.Fatalf("allowed result output = %#v, err %v", out, err)
	}
	if _, out, err := s.handleGetResult(ctx, nil, getResultInput{CallID: allowedID, Cursor: 8}); err != nil || out.(map[string]any)["status"] != "cursor_out_of_range" {
		t.Fatalf("beyond-end output = %#v, err %v", out, err)
	}

	blockedID, err := s.store.PutResult(ctx, "codemap", "other", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.handleGetResult(ctx, nil, getResultInput{CallID: blockedID}); err == nil || !strings.Contains(err.Error(), "out of scope") {
		t.Fatalf("out-of-scope result error = %v", err)
	}
	if _, _, err := s.handleGetResult(ctx, nil, getResultInput{CallID: blockedID, Cursor: 999}); err == nil || !strings.Contains(err.Error(), "out of scope") {
		t.Fatalf("out-of-scope beyond-end result error = %v", err)
	}

	expiredID, err := s.store.PutResult(ctx, "codemap", "search", []byte("expired"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec("UPDATE result_spool SET expires_at = ? WHERE call_id = ?", "2000-01-01T00:00:00Z", expiredID); err != nil {
		sqlDB.Close()
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	_, unknownOut, err := s.handleGetResult(ctx, nil, getResultInput{CallID: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	_, expiredOut, err := s.handleGetResult(ctx, nil, getResultInput{CallID: expiredID})
	if err != nil {
		t.Fatal(err)
	}
	unknown := unknownOut.(map[string]any)
	expired := expiredOut.(map[string]any)
	if unknown["status"] != "unavailable" || expired["status"] != unknown["status"] || expired["reason"] != unknown["reason"] {
		t.Fatalf("unknown = %#v, expired = %#v", unknown, expired)
	}
}

func TestHandleGetResultInfrastructureErrorsRemainErrors(t *testing.T) {
	s, _ := serverWithResultStore(t, nil)
	if err := s.store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.handleGetResult(context.Background(), nil, getResultInput{CallID: "any"}); err == nil || !strings.Contains(err.Error(), "read stored result") {
		t.Fatalf("closed-store error = %v", err)
	}
}

func TestHandleCallToolDetachUnknownServer(t *testing.T) {
	s := testServer(t)
	_, _, err := s.handleCallTool(context.Background(), nil, callInput{Tool: "ghost__tool", Detach: true})
	if err == nil || !strings.Contains(err.Error(), "unknown server") || !strings.Contains(err.Error(), "detach") {
		t.Fatalf("detached unknown-server error = %v", err)
	}
}

func TestHandleCallToolDetachOutOfScope(t *testing.T) {
	s := testScopedServer(t)
	_, _, err := s.handleCallTool(context.Background(), nil, callInput{Tool: "codemap__other", Detach: true})
	if err == nil || !strings.Contains(err.Error(), "out of scope") {
		t.Fatalf("expected out-of-scope error, got %v", err)
	}
}

func TestHandlePollResultValidationAndUnknown(t *testing.T) {
	s := testServer(t)
	if _, _, err := s.handlePollResult(context.Background(), nil, pollResultInput{}); err == nil || !strings.Contains(err.Error(), "callId") {
		t.Fatalf("missing callId error = %v", err)
	}
	_, out, err := s.handlePollResult(context.Background(), nil, pollResultInput{CallID: "never-issued"})
	if err != nil {
		t.Fatal(err)
	}
	unknown := out.(map[string]any)
	if unknown["status"] != "unknown" || unknown["callId"] != "never-issued" {
		t.Fatalf("unknown poll output = %#v", unknown)
	}
	if reason, _ := unknown["reason"].(string); !strings.Contains(reason, "restart") {
		t.Fatalf("unknown reason should explain restart semantics: %#v", unknown)
	}
}

func TestEffectiveCallTimeout(t *testing.T) {
	cases := []struct {
		name      string
		cfg       *config.Config
		timeoutMs int64
		want      time.Duration
	}{
		{"nil config default", nil, 0, 30 * time.Minute},
		{"unset uses config default", &config.Config{}, 0, 30 * time.Minute},
		{"below limit honored", &config.Config{}, 5_000, 5 * time.Second},
		{"clamped to call_timeout", &config.Config{CallTimeout: "1s"}, 60_000, time.Second},
		{"negative treated as unset", &config.Config{CallTimeout: "2m"}, -5, 2 * time.Minute},
		// Regression: a huge timeout_ms used to overflow time.Duration into a
		// negative value that escaped the clamp, producing an instantly-expired
		// deadline (and, via Call's transport-failure path, a needless session
		// invalidation + reconnect).
		{"huge value clamps instead of overflowing", &config.Config{}, 10_000_000_000_000, 30 * time.Minute},
		{"max int64 clamps instead of overflowing", &config.Config{CallTimeout: "1h"}, 1<<63 - 1, time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Server{cfg: c.cfg}
			if got := s.effectiveCallTimeout(c.timeoutMs); got != c.want {
				t.Fatalf("effectiveCallTimeout(%d) = %v, want %v", c.timeoutMs, got, c.want)
			}
			if got := s.effectiveCallTimeout(c.timeoutMs); got <= 0 {
				t.Fatalf("effectiveCallTimeout(%d) = %v, must always be positive", c.timeoutMs, got)
			}
		})
	}
}

func TestPollResultRegistrationAndCallSchemaOnWire(t *testing.T) {
	s, _ := serverWithResultStore(t, nil)
	client := connectServerClient(t, s.srv)
	tools, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*sdk.Tool{}
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool
	}
	poll, ok := byName["mcphub_poll_result"]
	if !ok {
		t.Fatal("mcphub_poll_result is not registered")
	}
	pollSchema, err := json.Marshal(poll.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(pollSchema, []byte(`"required":["callId"]`)) {
		t.Fatalf("callId is not required in poll wire schema: %s", pollSchema)
	}
	call, ok := byName["mcphub_call_tool"]
	if !ok {
		t.Fatal("mcphub_call_tool is not registered")
	}
	callSchema, err := json.Marshal(call.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"detach"`, `"timeout_ms"`} {
		if !bytes.Contains(callSchema, []byte(field)) {
			t.Fatalf("call wire schema missing %s: %s", field, callSchema)
		}
	}
	if !strings.Contains(call.Description, "detach") || !strings.Contains(call.Description, "mcphub_poll_result") {
		t.Fatalf("mcphub_call_tool description does not advertise the detached path: %q", call.Description)
	}
}

// TestDetachedCallWireFlow exercises the full long-running-call affordance
// over the wire: a synchronous call bounded by timeout_ms fails fast, a
// detached call returns an accepted receipt immediately, polls as pending
// while the downstream is still working, and finally yields the exact tool
// result — surviving well past what a client-side timeout would have allowed.
func TestDetachedCallWireFlow(t *testing.T) {
	release := make(chan struct{})
	downstream := sdk.NewServer(&sdk.Implementation{Name: "bg", Version: "1"}, nil)
	downstream.AddTool(
		&sdk.Tool{Name: "slow", InputSchema: map[string]any{"type": "object"}},
		func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			select {
			case <-release:
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "bg done"}}}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	)
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return downstream }, nil))
	defer httpServer.Close()
	// Unblock any still-gated handler before httpServer.Close, even when the
	// test fails partway; Close waits for outstanding requests.
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()

	st, err := store.Open(filepath.Join(t.TempDir(), "wire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := &config.Config{
		Expose:  config.ExposeLazy,
		Servers: map[string]config.Server{"bg": {URL: httpServer.URL, Transport: "http", Enabled: true}},
	}
	h := hub.New(cfg, st, nil)
	h.Connect(context.Background())
	defer h.Close()
	s := NewServer(cfg, h, st, nil)
	client := connectServerClient(t, s.srv)
	ctx := context.Background()

	// 1. Synchronous path: timeout_ms bounds the call from the gateway side.
	timedOut, err := client.CallTool(ctx, &sdk.CallToolParams{
		Name:      "mcphub_call_tool",
		Arguments: json.RawMessage(`{"tool":"bg__slow","timeout_ms":50}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !timedOut.IsError {
		t.Fatalf("timeout_ms-bounded call should fail, got %#v", timedOut)
	}
	if text := textContent(timedOut); !strings.Contains(text, "bg__slow") {
		t.Fatalf("timeout error text = %q", text)
	}

	// 2. Detached path: an accepted receipt comes back immediately.
	accepted, err := client.CallTool(ctx, &sdk.CallToolParams{
		Name:      "mcphub_call_tool",
		Arguments: json.RawMessage(`{"tool":"bg__slow","detach":true,"timeout_ms":60000}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.IsError {
		t.Fatalf("detached call rejected: %s", textContent(accepted))
	}
	receipt, ok := accepted.StructuredContent.(map[string]any)
	if !ok || receipt["status"] != "accepted" {
		t.Fatalf("detached receipt = %#v", accepted.StructuredContent)
	}
	callID, _ := receipt["callId"].(string)
	if callID == "" || receipt["namespaced"] != "bg__slow" {
		t.Fatalf("detached receipt = %#v", receipt)
	}

	poll := func() *sdk.CallToolResult {
		t.Helper()
		res, err := client.CallTool(ctx, &sdk.CallToolParams{
			Name:      "mcphub_poll_result",
			Arguments: json.RawMessage(fmt.Sprintf(`{"callId":%q}`, callID)),
		})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	// 3. Still running: poll reports pending.
	pending := poll()
	out, ok := pending.StructuredContent.(map[string]any)
	if !ok || out["status"] != "pending" {
		t.Fatalf("pending poll = %#v", pending.StructuredContent)
	}

	// 4. Let the downstream finish; polling now returns the tool result itself.
	released = true
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	var final *sdk.CallToolResult
	for time.Now().Before(deadline) {
		res := poll()
		if status, ok := res.StructuredContent.(map[string]any); ok && status["status"] == "pending" {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		final = res
		break
	}
	if final == nil {
		t.Fatal("detached call never completed")
	}
	if final.IsError || textContent(final) != "bg done" {
		t.Fatalf("final detached result = IsError=%v text=%q", final.IsError, textContent(final))
	}

	// 5. Re-polling a completed call is idempotent.
	if again := poll(); textContent(again) != "bg done" {
		t.Fatalf("re-poll text = %q", textContent(again))
	}
}

// TestHandlePollResultOutOfScope verifies mcphub_poll_result enforces the same
// scope contract as mcphub_get_result: a scoped gateway cannot collect a
// detached call whose server/tool its agent is not allowed to reach, even
// with a valid callId.
func TestHandlePollResultOutOfScope(t *testing.T) {
	release := make(chan struct{})
	downstream := sdk.NewServer(&sdk.Implementation{Name: "bg", Version: "1"}, nil)
	downstream.AddTool(
		&sdk.Tool{Name: "slow", InputSchema: map[string]any{"type": "object"}},
		func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			select {
			case <-release:
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "done"}}}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	)
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return downstream }, nil))
	defer httpServer.Close()
	cfg := &config.Config{Servers: map[string]config.Server{
		"bg": {URL: httpServer.URL, Transport: "http", Enabled: true},
	}}
	h := hub.New(cfg, nil, nil)
	h.Connect(context.Background())
	defer h.Close()
	// Declared after h.Close so it runs first: the gated downstream handler
	// must be released before session teardown waits on it.
	defer close(release)

	owner := &Server{hub: h, cfg: cfg} // unscoped gateway starts the call
	_, out, err := owner.handleCallTool(context.Background(), nil, callInput{Tool: "bg__slow", Detach: true})
	if err != nil {
		t.Fatal(err)
	}
	callID, _ := out.(map[string]any)["callId"].(string)
	if callID == "" {
		t.Fatalf("no callId in detach receipt: %#v", out)
	}

	scoped := &Server{hub: h, cfg: cfg, scope: &agentScope{servers: map[string]bool{"codemap": true}}}
	if _, _, err := scoped.handlePollResult(context.Background(), nil, pollResultInput{CallID: callID}); err == nil || !strings.Contains(err.Error(), "out of scope") {
		t.Fatalf("cross-scope poll error = %v, want out-of-scope rejection", err)
	}

	// The gateway whose scope covers the call still polls normally.
	_, ownOut, err := owner.handlePollResult(context.Background(), nil, pollResultInput{CallID: callID})
	if err != nil {
		t.Fatal(err)
	}
	if status := ownOut.(map[string]any)["status"]; status != "pending" {
		t.Fatalf("owner poll status = %v, want pending", status)
	}
}
