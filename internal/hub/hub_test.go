package hub

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

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
	h.record(ctx, "live", "echo", "live__echo", 0, nil, 0, 0, nil)
}

func TestConnectMatchingDoesNotActivateExcludedServers(t *testing.T) {
	var allowedRequests atomic.Int32
	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		allowedRequests.Add(1)
		http.Error(w, "fixture unavailable", http.StatusServiceUnavailable)
	}))
	defer allowed.Close()

	var excludedRequests atomic.Int32
	excluded := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		excludedRequests.Add(1)
		http.Error(w, "must remain inactive", http.StatusServiceUnavailable)
	}))
	defer excluded.Close()

	h := New(&config.Config{Servers: map[string]config.Server{
		"allowed":  {URL: allowed.URL, Transport: "http", Enabled: true},
		"excluded": {URL: excluded.URL, Transport: "http", Enabled: true},
	}}, nil, nil)
	h.ConnectMatching(context.Background(), func(name string) bool { return name == "allowed" })
	defer h.Close()

	downstreams := h.Downstreams()
	if len(downstreams) != 1 || downstreams[0].Name != "allowed" {
		t.Fatalf("scoped downstreams = %#v, want only allowed", downstreams)
	}
	if allowedRequests.Load() == 0 {
		t.Fatal("allowed server was not activated")
	}
	if got := excludedRequests.Load(); got != 0 {
		t.Fatalf("excluded server received %d requests", got)
	}
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
	h.record(ctx, "s", "t", "s__t", time.Millisecond, nil, 10, 2, &mcp.CallToolResult{})
	h.record(ctx, "s", "t", "s__t", time.Millisecond, nil, 10, 64,
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
	h.record(ctx, "s", "t", "s__t", time.Millisecond, context.Canceled, 0, 0, nil)

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
		{
			"http remote with auth headers",
			config.Server{URL: "https://srv.example.com", Transport: "http", Headers: map[string]string{"Authorization": "Bearer tok"}},
			func(tr mcp.Transport) bool {
				st, ok := tr.(*mcp.StreamableClientTransport)
				return ok && st.Endpoint == "https://srv.example.com" && st.HTTPClient != nil
			},
		},
		{
			"localhost https gets custom client (self-signed cert)",
			config.Server{URL: "https://127.0.0.1:27124/mcp/", Transport: "http"},
			func(tr mcp.Transport) bool {
				st, ok := tr.(*mcp.StreamableClientTransport)
				return ok && st.HTTPClient != nil
			},
		},
		{
			"localhost https with headers",
			config.Server{URL: "https://localhost:9999", Transport: "http", Headers: map[string]string{"Authorization": "Bearer x"}},
			func(tr mcp.Transport) bool {
				st, ok := tr.(*mcp.StreamableClientTransport)
				return ok && st.HTTPClient != nil
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

func TestHeaderRoundTripper(t *testing.T) {
	rt := &headerRoundTripper{
		base:    &mockRoundTripper{},
		headers: map[string]string{"Authorization": "Bearer secret", "X-Custom": "val"},
	}
	req := httptest.NewRequest("POST", "https://srv.example.com", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer secret" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer secret")
	}
	if got := req.Header.Get("X-Custom"); got != "val" {
		t.Errorf("X-Custom = %q, want %q", got, "val")
	}
}

func TestIsLocalhostHTTPS(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://127.0.0.1:27124/mcp/", true},
		{"https://localhost:9999", true},
		{"https://[::1]:8080", true},
		{"http://127.0.0.1:27124", false},
		{"https://srv.example.com", false},
		{"https://192.168.1.1", false},
		{"not a url", false},
	}
	for _, c := range cases {
		if got := isLocalhostHTTPS(c.url); got != c.want {
			t.Errorf("isLocalhostHTTPS(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// mockRoundTripper is a no-op http.RoundTripper for testing header injection.
type mockRoundTripper struct{}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
}

func openHubStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "results.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, path
}

func spoolRows(t *testing.T, path string) int {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	var count int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM result_spool").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func connectInMemoryClient(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "hub-test", Version: "1"}, nil)
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

func inMemoryDownstream(t *testing.T, result *mcp.CallToolResult) (*mcp.ClientSession, *mcp.Tool) {
	t.Helper()
	tool := &mcp.Tool{Name: "large", InputSchema: map[string]any{"type": "object"}}
	server := mcp.NewServer(&mcp.Implementation{Name: "memory", Version: "1"}, nil)
	server.AddTool(tool, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return result, nil
	})
	return connectInMemoryClient(t, server), tool
}

func TestMountPreservesDownstreamToolMetadata(t *testing.T) {
	destructive := false
	openWorld := false
	downstreamServer := mcp.NewServer(&mcp.Implementation{Name: "bob", Version: "1"}, nil)
	downstreamServer.AddTool(&mcp.Tool{
		Meta:        mcp.Meta{"audience": []any{"agent", "human"}},
		Name:        "inspect",
		Title:       "Inspect a Bob workspace",
		Description: "Inspect repository state without changing it.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"workspace": map[string]any{"type": "string"}},
		},
		OutputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
		},
		Annotations: &mcp.ToolAnnotations{
			Title:           "Inspect workspace",
			ReadOnlyHint:    true,
			DestructiveHint: &destructive,
			IdempotentHint:  true,
			OpenWorldHint:   &openWorld,
		},
		Icons: []mcp.Icon{{
			Source:   "data:image/svg+xml;base64,PHN2Zy8+",
			MIMEType: "image/svg+xml",
			Sizes:    []string{"any"},
		}},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"ok": true}}, nil
	})
	downstreamSession := connectInMemoryClient(t, downstreamServer)
	downstreamList, err := downstreamSession.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(downstreamList.Tools) != 1 {
		t.Fatalf("downstream tools = %d, want 1", len(downstreamList.Tools))
	}
	source := downstreamList.Tools[0]

	h := New(&config.Config{}, nil, nil)
	h.downstreams = []*Downstream{{Name: "bob", session: downstreamSession, Tools: downstreamList.Tools}}
	gateway := mcp.NewServer(&mcp.Implementation{Name: "gateway", Version: "1"}, nil)
	if mounted := h.Mount(gateway); mounted != 1 {
		t.Fatalf("mounted tools = %d, want 1", mounted)
	}
	gatewaySession := connectInMemoryClient(t, gateway)
	gatewayList, err := gatewaySession.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gatewayList.Tools) != 1 {
		t.Fatalf("gateway tools = %d, want 1", len(gatewayList.Tools))
	}
	got := gatewayList.Tools[0]
	if got.Name != "bob__inspect" {
		t.Errorf("name = %q, want bob__inspect", got.Name)
	}
	if got.Description != "[bob] "+source.Description {
		t.Errorf("description = %q, want prefixed downstream description", got.Description)
	}
	if got.Title != source.Title {
		t.Errorf("title = %q, want %q", got.Title, source.Title)
	}
	if !reflect.DeepEqual(got.InputSchema, source.InputSchema) {
		t.Errorf("input schema changed: got %#v, want %#v", got.InputSchema, source.InputSchema)
	}
	if !reflect.DeepEqual(got.OutputSchema, source.OutputSchema) {
		t.Errorf("output schema changed: got %#v, want %#v", got.OutputSchema, source.OutputSchema)
	}
	if !reflect.DeepEqual(got.Annotations, source.Annotations) {
		t.Errorf("annotations changed: got %#v, want %#v", got.Annotations, source.Annotations)
	}
	if !reflect.DeepEqual(got.Icons, source.Icons) {
		t.Errorf("icons changed: got %#v, want %#v", got.Icons, source.Icons)
	}
	if !reflect.DeepEqual(got.Meta, source.Meta) {
		t.Errorf("meta changed: got %#v, want %#v", got.Meta, source.Meta)
	}
}

func TestMountMatchingBudgetedIsDeterministicAndPreservesMetadata(t *testing.T) {
	readOnly := true
	downstreamServer := mcp.NewServer(&mcp.Implementation{Name: "budget", Version: "1"}, nil)
	downstreamServer.AddTool(&mcp.Tool{
		Name:        "a_huge",
		Description: strings.Repeat("large definition ", 200),
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	downstreamServer.AddTool(&mcp.Tool{
		Meta:        mcp.Meta{"audience": []any{"agent"}},
		Name:        "b_compact",
		Title:       "Compact tool",
		Description: "Small enough to fit after the earlier oversized candidate is skipped.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"query": map[string]any{"type": "string"}},
		},
		OutputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
		},
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
		Icons:       []mcp.Icon{{Source: "data:image/svg+xml;base64,PHN2Zy8+"}},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	downstreamSession := connectInMemoryClient(t, downstreamServer)
	list, err := downstreamSession.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var compact *mcp.Tool
	for _, tool := range list.Tools {
		if tool.Name == "b_compact" {
			compact = tool
			break
		}
	}
	if compact == nil {
		t.Fatal("compact downstream tool not found")
	}
	encodedCompact, err := json.Marshal(namespacedTool("fixture", compact))
	if err != nil {
		t.Fatal(err)
	}

	h := New(&config.Config{}, nil, nil)
	h.downstreams = []*Downstream{{Name: "fixture", session: downstreamSession, Tools: list.Tools}}
	gateway := mcp.NewServer(&mcp.Implementation{Name: "gateway", Version: "1"}, nil)
	report, err := h.MountMatchingBudgeted(gateway, func(string) bool { return true }, len(encodedCompact))
	if err != nil {
		t.Fatal(err)
	}
	if report.EligibleTools != 2 || report.AdvertisedTools != 1 || report.OmittedTools != 1 {
		t.Fatalf("budget report = %+v", report)
	}
	if report.AdvertisedDefinitionBytes != len(encodedCompact) || report.EligibleDefinitionBytes <= report.AdvertisedDefinitionBytes {
		t.Fatalf("budget byte accounting = %+v, compact=%d", report, len(encodedCompact))
	}
	if report.AdvertisedEstimatedTokens != (len(encodedCompact)+3)/4 {
		t.Fatalf("estimated tokens = %d, want %d", report.AdvertisedEstimatedTokens, (len(encodedCompact)+3)/4)
	}

	client := connectInMemoryClient(t, gateway)
	mounted, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mounted.Tools) != 1 || mounted.Tools[0].Name != "fixture__b_compact" {
		t.Fatalf("budget selected tools = %#v", mounted.Tools)
	}
	got := mounted.Tools[0]
	if got.Title != compact.Title ||
		!reflect.DeepEqual(got.InputSchema, compact.InputSchema) ||
		!reflect.DeepEqual(got.OutputSchema, compact.OutputSchema) ||
		!reflect.DeepEqual(got.Annotations, compact.Annotations) ||
		!reflect.DeepEqual(got.Icons, compact.Icons) ||
		!reflect.DeepEqual(got.Meta, compact.Meta) {
		t.Fatalf("budgeted mount dropped metadata:\n got  %#v\n want %#v", got, compact)
	}
}

func TestMountMatchingBudgetedZeroAdvertisesNoDownstreamTools(t *testing.T) {
	session, tool := inMemoryDownstream(t, &mcp.CallToolResult{})
	h := New(&config.Config{}, nil, nil)
	h.downstreams = []*Downstream{{Name: "memory", session: session, Tools: []*mcp.Tool{tool}}}
	gateway := mcp.NewServer(&mcp.Implementation{Name: "gateway", Version: "1"}, nil)

	report, err := h.MountMatchingBudgeted(gateway, func(string) bool { return true }, 0)
	if err != nil {
		t.Fatal(err)
	}
	if report.EligibleTools != 1 || report.AdvertisedTools != 0 || report.OmittedTools != 1 || report.AdvertisedDefinitionBytes != 0 {
		t.Fatalf("zero-budget report = %+v", report)
	}
	client := connectInMemoryClient(t, gateway)
	list, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 0 {
		t.Fatalf("zero budget advertised %d downstream tools", len(list.Tools))
	}
}

func TestBoundedLosslessMountedCallReconstructsExactResult(t *testing.T) {
	st, _ := openHubStore(t)
	cfg := &config.Config{ResponseBudget: "900B"}
	original := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("世界-data-", 600)}},
		StructuredContent: map[string]any{
			"kind": "large",
			"rows": []any{1, "two", true},
		},
		IsError: true,
	}
	expected, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	downstreamSession, tool := inMemoryDownstream(t, original)
	h := New(cfg, st, nil)
	h.downstreams = []*Downstream{{Name: "memory", session: downstreamSession, Tools: []*mcp.Tool{tool}}}

	gateway := mcp.NewServer(&mcp.Implementation{Name: "gateway", Version: "1"}, nil)
	h.MountMatching(gateway, func(string) bool { return true })
	gatewayClient := connectInMemoryClient(t, gateway)
	receiptResult, err := gatewayClient.CallTool(context.Background(), &mcp.CallToolParams{Name: "memory__large"})
	if err != nil {
		t.Fatal(err)
	}
	if !receiptResult.IsError {
		t.Fatal("bounded receipt must preserve the downstream IsError flag")
	}
	receipt, ok := receiptResult.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured receipt type = %T", receiptResult.StructuredContent)
	}
	callID, _ := receipt["callId"].(string)
	if callID == "" || receipt["namespaced"] != "memory__large" {
		t.Fatalf("receipt = %#v", receipt)
	}
	if got := int(receipt["originalBytes"].(float64)); got != len(expected) {
		t.Fatalf("originalBytes = %d, want %d", got, len(expected))
	}
	receiptBytes, err := json.Marshal(receiptResult)
	if err != nil {
		t.Fatal(err)
	}
	if len(receiptBytes) > cfg.ResponseBudgetBytes() {
		t.Fatalf("serialized receipt = %d bytes, budget = %d", len(receiptBytes), cfg.ResponseBudgetBytes())
	}

	var rebuilt []byte
	var cursor int64
	for {
		page, err := st.ReadResultPage(context.Background(), callID, cursor, 257)
		if err != nil {
			t.Fatal(err)
		}
		rebuilt = append(rebuilt, page.Data...)
		cursor = page.NextCursor
		if page.Done {
			break
		}
	}
	if !bytes.Equal(rebuilt, expected) {
		t.Fatal("stored mounted-tool result did not reconstruct byte-for-byte")
	}
	recent, err := st.RecentCalls(context.Background(), 1)
	if err != nil || len(recent) != 1 || recent[0].ResultBytes != int64(len(expected)) {
		t.Fatalf("telemetry result bytes = %+v, err %v", recent, err)
	}
}

func TestFinalizeCallCompactsReceiptToMinimumBudget(t *testing.T) {
	st, _ := openHubStore(t)
	cfg := &config.Config{ResponseBudget: "512B"}
	h := New(cfg, st, nil)
	res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("payload-", 500)}}}
	longName := strings.Repeat("downstream-", 200)
	got := h.finalizeCall(context.Background(), longName, longName, longName+"__"+longName, 0, 0, res)
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > config.MinResponseBudgetBytes {
		t.Fatalf("compact receipt = %d bytes, budget = %d", len(encoded), config.MinResponseBudgetBytes)
	}
	receipt, ok := got.StructuredContent.(resultReceipt)
	if !ok || receipt.CallID == "" {
		t.Fatalf("compact receipt missing retrieval call ID: %#v", got.StructuredContent)
	}
	if receipt.Server != "" || receipt.Tool != "" || receipt.Namespaced != "" {
		t.Fatalf("oversized origin metadata was not removed: %+v", receipt)
	}
}

func TestFinalizeCallCompatibilityPathsAreUnchangedAndSpoolFree(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		res  *mcp.CallToolResult
	}{
		{
			name: "small",
			cfg:  &config.Config{ResponseBudget: "4KB"},
			res:  &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "small"}}},
		},
		{
			name: "verbatim",
			cfg:  &config.Config{ResponseBudget: "1B", Verbatim: true},
			res:  &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("v", 400)}}},
		},
		{
			name: "unlimited",
			cfg:  &config.Config{ResponseBudget: "0"},
			res:  &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("u", 400)}}},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			st, path := openHubStore(t)
			h := New(test.cfg, st, nil)
			before, err := json.Marshal(test.res)
			if err != nil {
				t.Fatal(err)
			}
			got := h.finalizeCall(context.Background(), "s", "t", "s__t", time.Millisecond, 0, test.res)
			after, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.res || !bytes.Equal(after, before) {
				t.Fatal("compatibility path changed the original result")
			}
			if count := spoolRows(t, path); count != 0 {
				t.Fatalf("spool rows = %d, want 0", count)
			}
		})
	}
}

func TestFinalizeCallBudgetsSerializedNonTextAndUTF8Preview(t *testing.T) {
	st, _ := openHubStore(t)
	h := New(&config.Config{ResponseBudget: "900B"}, st, nil)
	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: strings.Repeat("é界", 80)},
			&mcp.ImageContent{Data: bytes.Repeat([]byte("A"), 2048), MIMEType: "image/png"},
		},
	}
	got := h.finalizeCall(context.Background(), "s", "media", "s__media", 0, 0, res)
	receipt, ok := got.StructuredContent.(resultReceipt)
	if !ok {
		t.Fatalf("non-text bytes did not trigger a receipt: %T", got.StructuredContent)
	}
	if receipt.OriginalBytes <= 2048 || receipt.CallID == "" {
		t.Fatalf("receipt sizes = %+v", receipt)
	}
	if receipt.Preview == "" {
		t.Fatal("UTF-8 text preview was omitted despite available receipt headroom")
	}
	if !utf8.ValidString(receipt.Preview) {
		t.Fatalf("preview is not valid UTF-8: %q", receipt.Preview)
	}
}

func TestFinalizeCallMarshalAndStoreFailuresFailOpen(t *testing.T) {
	t.Run("marshal", func(t *testing.T) {
		st, path := openHubStore(t)
		h := New(&config.Config{ResponseBudget: "1B"}, st, nil)
		res := &mcp.CallToolResult{StructuredContent: make(chan int)}
		if got := h.finalizeCall(context.Background(), "s", "t", "s__t", 0, 0, res); got != res {
			t.Fatal("marshal failure did not return original result")
		}
		if count := spoolRows(t, path); count != 0 {
			t.Fatalf("spool rows = %d, want 0", count)
		}
	})
	t.Run("store", func(t *testing.T) {
		st, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		h := New(&config.Config{ResponseBudget: "1B"}, st, nil)
		res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", 200)}}}
		if got := h.finalizeCall(context.Background(), "s", "t", "s__t", 0, 0, res); got != res {
			t.Fatal("store failure did not return original result")
		}
	})
}

func TestCallDoesNotReplayUnknownOutcome(t *testing.T) {
	st, _ := openHubStore(t)
	downstream := mcp.NewServer(&mcp.Implementation{Name: "retry", Version: "1"}, nil)
	var downstreamCalls atomic.Int32
	downstream.AddTool(
		&mcp.Tool{Name: "effect", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			downstreamCalls.Add(1)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "effect completed"}}}, nil
		},
	)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return downstream }, nil)
	var droppedFirstResponse atomic.Bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			http.Error(w, "read body", http.StatusInternalServerError)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte(`"method":"tools/call"`)) && droppedFirstResponse.CompareAndSwap(false, true) {
			// Execute the downstream request, then discard its successful response.
			// From the caller's perspective this is a transport failure after the
			// effect may already have happened.
			recorder := httptest.NewRecorder()
			mcpHandler.ServeHTTP(recorder, r)
			http.Error(w, "response lost after effect", http.StatusInternalServerError)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	cfg := &config.Config{
		ResponseBudget: "700B",
		Servers: map[string]config.Server{
			"retry": {URL: httpServer.URL, Transport: "http", Enabled: true},
		},
	}
	h := New(cfg, st, nil)
	h.Connect(context.Background())
	defer h.Close()
	got, err := h.Call(context.Background(), "retry", "effect", nil)
	if err == nil || !strings.Contains(err.Error(), "outcome unknown") || !strings.Contains(err.Error(), "request was not retried") {
		t.Fatalf("Call() result = %#v, %v; want bounded outcome-unknown error", got, err)
	}
	if got != nil {
		t.Fatalf("uncertain call returned a result: %#v", got)
	}
	if calls := downstreamCalls.Load(); calls != 1 {
		t.Fatalf("downstream effect calls = %d, want exactly 1", calls)
	}
	if d := h.downstream("retry"); d == nil || !d.Connected() {
		t.Fatalf("connection was not restored for future calls: %#v", d)
	}
	totals, err := st.Totals(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if totals.Calls != 1 || totals.Errors != 1 {
		t.Fatalf("telemetry calls=%d errors=%d, want 1/1", totals.Calls, totals.Errors)
	}
}

// TestConcurrentReconnectsDoNotStampede is a regression test: several
// in-flight calls can observe the same transport failure at once (detached
// calls especially, whose contexts outlive the requesting client). Immediate
// reconnects must be serialized per server so they do not each open a new
// session and close the one the previous reconnect just established.
func TestConcurrentReconnectsDoNotStampede(t *testing.T) {
	downstream := mcp.NewServer(&mcp.Implementation{Name: "dedupe", Version: "1"}, nil)
	downstream.AddTool(
		&mcp.Tool{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
		},
	)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return downstream }, nil)
	var initializes atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			http.Error(w, "read body", http.StatusInternalServerError)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte(`"method":"initialize"`)) {
			initializes.Add(1)
		}
		mcpHandler.ServeHTTP(w, r)
	})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	cfg := &config.Config{Servers: map[string]config.Server{
		"dedupe": {URL: httpServer.URL, Transport: "http", Enabled: true},
	}}
	h := New(cfg, nil, nil)
	h.Connect(context.Background())
	defer h.Close()
	failed := h.downstream("dedupe")
	if failed == nil || !failed.Connected() {
		t.Fatal("setup: downstream did not connect")
	}
	before := initializes.Load()

	// Simulate concurrent calls all hitting a transport failure on the same
	// session and each demanding an immediate reconnect.
	h.invalidateDownstream(failed)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !h.ReconnectOne(context.Background(), "dedupe") {
				t.Error("ReconnectOne reported failure during concurrent recovery")
			}
		}()
	}
	wg.Wait()

	if got := initializes.Load() - before; got != 1 {
		t.Fatalf("concurrent reconnects opened %d new sessions, want exactly 1", got)
	}
	res, err := h.Call(context.Background(), "dedupe", "ping", nil)
	if err != nil {
		t.Fatalf("post-recovery call failed: %v", err)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; text != "pong" {
		t.Fatalf("post-recovery call text = %q", text)
	}
}

// TestInvalidateDownstreamIsIdentityScoped is a regression test: a failure
// observed on an already-replaced session must not mark the fresh entry a
// concurrent reconnect installed under the same name as disconnected.
func TestInvalidateDownstreamIsIdentityScoped(t *testing.T) {
	h := New(&config.Config{}, nil, nil)
	stale := &Downstream{Name: "srv"}
	session, tool := inMemoryDownstream(t, &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}})
	fresh := &Downstream{Name: "srv", session: session, Tools: []*mcp.Tool{tool}}
	h.downstreams = []*Downstream{fresh}

	h.invalidateDownstream(stale) // stale entry is no longer in the set
	if !fresh.Connected() {
		t.Fatal("invalidating a replaced entry disconnected the fresh session under the same name")
	}
	h.invalidateDownstream(fresh)
	if fresh.Connected() {
		t.Fatal("invalidating the current entry must disconnect it")
	}
}

// TestReconnectOneDoesNotBlockOnStaleSessionTeardown is a regression test:
// after a gateway-side timeout the stale session's teardown can block for the
// transport's grace period (a stdio child mid-call ignores the close). That
// teardown must happen off the reconnecting caller's path and outside h.mu,
// or a single timed-out call stalls its own error response and every other
// hub operation with it.
func TestReconnectOneDoesNotBlockOnStaleSessionTeardown(t *testing.T) {
	downstream := mcp.NewServer(&mcp.Implementation{Name: "slowclose", Version: "1"}, nil)
	downstream.AddTool(
		&mcp.Tool{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
		},
	)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return downstream }, nil)
	releaseClose := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			<-releaseClose // simulate a session whose teardown hangs while the peer is busy
		}
		mcpHandler.ServeHTTP(w, r)
	})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	cfg := &config.Config{Servers: map[string]config.Server{
		"slowclose": {URL: httpServer.URL, Transport: "http", Enabled: true},
	}}
	h := New(cfg, nil, nil)
	h.Connect(context.Background())
	defer h.Close()
	// Declared last so it runs first: every blocked DELETE (the stale session's
	// async teardown, h.Close, httpServer.Close) must be released before the
	// other cleanups wait on it.
	defer close(releaseClose)
	failed := h.downstream("slowclose")
	if failed == nil || !failed.Connected() {
		t.Fatal("setup: downstream did not connect")
	}

	h.invalidateDownstream(failed)
	start := time.Now()
	if !h.ReconnectOne(context.Background(), "slowclose") {
		t.Fatal("ReconnectOne failed")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("ReconnectOne blocked %v on the stale session's teardown", elapsed)
	}
	res, err := h.Call(context.Background(), "slowclose", "ping", nil)
	if err != nil {
		t.Fatalf("post-reconnect call failed: %v", err)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; text != "pong" {
		t.Fatalf("post-reconnect call text = %q", text)
	}
}

// TestCloseBoundsShutdownAndSkipsReconnect pins the bounded-SIGTERM contract:
// Close must return promptly while a detached call is still blocked on its
// downstream (the shutdown watcher cancels the call's background context),
// and the resulting transport failure must not respawn the downstream — a
// reconnect during teardown would leave a session nothing ever closes. The
// call still surfaces the honest outcome-unknown error without a retry.
func TestCloseBoundsShutdownAndSkipsReconnect(t *testing.T) {
	var reconnects atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reconnects.Add(1)
		http.Error(w, "no reconnect during shutdown", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	release := make(chan struct{})
	defer close(release)
	session, tool := gatedDownstream(t, release, &mcp.CallToolResult{})
	// cfg knows "bg", so any reconnect attempt would dial upstream.
	h := New(&config.Config{Servers: map[string]config.Server{
		"bg": {URL: upstream.URL, Transport: "http", Enabled: true},
	}}, nil, nil)
	h.downstreams = []*Downstream{{Name: "bg", session: session, Tools: []*mcp.Tool{tool}}}

	id, err := h.StartDetached(context.Background(), "bg", "slow", nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if call, ok := h.PollDetached(id); !ok || call.Status != DetachedPending {
		t.Fatalf("setup: detached call not pending: %+v ok=%v", call, ok)
	}

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		_ = h.Close()
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not return within 1s while a detached call was blocked downstream")
	}

	failed := waitDetachedStatus(t, h, id, DetachedFailed)
	if !strings.Contains(failed.Err, "outcome unknown") || !strings.Contains(failed.Err, "no reconnect was attempted") {
		t.Fatalf("failed.Err = %q, want shutdown-path outcome-unknown error", failed.Err)
	}
	if got := reconnects.Load(); got != 0 {
		t.Fatalf("shutdown transport failure attempted %d reconnects, want 0", got)
	}
	if h.ReconnectOne(context.Background(), "bg") {
		t.Fatal("ReconnectOne must refuse to respawn a downstream after Close")
	}
	if got := reconnects.Load(); got != 0 {
		t.Fatalf("post-Close ReconnectOne dialed the downstream %d times, want 0", got)
	}
}

func TestCanonicalToolCollapsesServerPrefixStutter(t *testing.T) {
	h := New(&config.Config{}, nil, nil)
	h.downstreams = []*Downstream{
		{Name: "hitspec", Tools: []*mcp.Tool{{Name: "hitspec_fetch"}}},
		{Name: "live", Tools: []*mcp.Tool{{Name: "echo"}, {Name: "live_echo"}}},
	}
	if name, _, ok := h.CanonicalTool("hitspec", "hitspec_fetch"); !ok || name != "hitspec_fetch" {
		t.Errorf("exact name should resolve to itself, got %q ok=%v", name, ok)
	}
	if name, tool, ok := h.CanonicalTool("hitspec", "fetch"); !ok || name != "hitspec_fetch" || tool.Name != "hitspec_fetch" {
		t.Errorf("collapsed alias should resolve to the prefixed tool, got %q ok=%v", name, ok)
	}
	// An exact downstream name must never be shadowed by prefix expansion.
	if name, _, ok := h.CanonicalTool("live", "echo"); !ok || name != "echo" {
		t.Errorf("exact name must win over the prefixed variant, got %q ok=%v", name, ok)
	}
	if _, _, ok := h.CanonicalTool("hitspec", "nope"); ok {
		t.Error("unknown tool should not resolve")
	}
}
