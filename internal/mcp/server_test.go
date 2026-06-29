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
