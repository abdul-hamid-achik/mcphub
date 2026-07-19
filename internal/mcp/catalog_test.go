package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/hub"
)

func routingFixture() []toolMatch {
	return []toolMatch{
		{
			Namespaced:        "hitspec__hitspec_fetch",
			Server:            "hitspec",
			Tool:              "hitspec_fetch",
			Description:       "Fetch one direct HTTP URL or one saved request as raw, text, Markdown, or JSON.",
			ServerDescription: "Bounded HTTP fetches and saved-request validation",
			Tags:              []string{"web", "http", "markdown"},
			UseWhen:           []string{"fetch a public HTTP URL as raw, text, Markdown, or JSON"},
		},
		{
			Namespaced:        "cortex__cortex_investigate",
			Server:            "cortex",
			Tool:              "cortex_investigate",
			Description:       "Investigate a task using multiple evidence sources.",
			ServerDescription: "Evidence-guided agent kernel",
			Tags:              []string{"research", "orchestration"},
			UseWhen:           []string{"analyze sources from codebases, databases, and CLI results"},
		},
		{
			Namespaced:        "bob__bob_plan",
			Server:            "bob",
			Tool:              "bob_plan",
			Title:             "Plan repository construction",
			Description:       "Plan a repository change from inspected state.",
			ServerDescription: "Deterministic repository factory",
			Tags:              []string{"builder", "code"},
			UseWhen:           []string{"plan or implement a feature in a repository"},
		},
	}
}

func TestRankCatalogPrefersBobPlanAcrossRealMultiToolShape(t *testing.T) {
	common := toolMatch{
		Server:            "bob",
		ServerDescription: "Deterministic repository factory and lifecycle reconciler",
		Tags:              []string{"builder", "code"},
		UseWhen:           []string{"inspect or plan a repository feature before implementation"},
	}
	tools := []struct {
		name, title, description string
	}{
		{"bob_check", "Check Bob-managed repository convergence", "Return a compact convergence and lock-drift summary."},
		{"bob_inspect", "Inspect a Bob workspace", "Summarize manifest and drift state."},
		{"bob_plan", "Plan repository construction", "Return a bounded deterministic action list and digest."},
		{"bob_recipe_describe", "Describe an embedded Bob recipe", "Describe the deterministic built-in recipe contract."},
		{"bob_stats", "Summarize local Bob usage", "Return aggregate opt-in local telemetry."},
		{"bob_validate_manifest", "Validate a Bob manifest", "Strictly validate one manifest source."},
	}
	entries := make([]toolMatch, 0, len(tools))
	for _, tool := range tools {
		entry := common
		entry.Tool = tool.name
		entry.Namespaced = "bob__" + tool.name
		entry.Title = tool.title
		entry.Description = tool.description
		entries = append(entries, entry)
	}

	ranked := rankCatalog("desarrollar una feature en este repositorio", entries)
	if len(ranked) == 0 || ranked[0].Namespaced != "bob__bob_plan" {
		t.Fatalf("real-shape Bob routing = %+v, want bob__bob_plan first", ranked)
	}
	if len(ranked) > 1 && ranked[0].Score == ranked[1].Score {
		t.Fatalf("bob_plan remained ambiguous: %+v", ranked[:2])
	}
}

func TestRankCatalogDistinguishesArtifactReadFromSave(t *testing.T) {
	common := toolMatch{
		Server:            "fcheap",
		ServerDescription: "Persistent file.cheap artifact stash with indexed search and retrieval",
		Tags:              []string{"artifacts", "files", "search"},
		UseWhen:           []string{"Search, inspect, retrieve, or manage artifacts previously saved in file.cheap."},
	}
	entries := []toolMatch{
		common,
		common,
		common,
	}
	entries[0].Namespaced, entries[0].Tool, entries[0].Description, entries[0].InputFields = "fcheap__fcheap_info", "fcheap_info", "Get detailed info about a stash including file list and metadata.", []string{"id"}
	entries[1].Namespaced, entries[1].Tool, entries[1].Description, entries[1].InputFields = "fcheap__fcheap_save", "fcheap_save", "Save a file or directory to the stash vault. Returns the stash ID and manifest.", []string{"path"}
	entries[2].Namespaced, entries[2].Tool, entries[2].Description, entries[2].InputFields = "fcheap__fcheap_drop", "fcheap_drop", "Permanently delete a stash and all its files.", []string{"id", "force"}

	ranked := rankCatalog("Retrieve and inspect a saved artifact by its exact ID", entries)
	if len(ranked) == 0 || ranked[0].Namespaced != "fcheap__fcheap_info" {
		t.Fatalf("artifact read routing = %+v, want fcheap__fcheap_info first", ranked)
	}
	if len(ranked) > 1 && ranked[0].Score == ranked[1].Score {
		t.Fatalf("artifact read remained ambiguous: %+v", ranked[:2])
	}
	for _, term := range ranked[0].MatchedTerms {
		if term == "it" {
			t.Fatalf("canonicalized stop word leaked into routing evidence: %+v", ranked[0])
		}
	}
}

func TestRankCatalogRoutesNaturalLanguageContext(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "spanish web capture",
			query: "Estoy investigando una PÁGINA web; necesito bajar la URL como Markdown",
			want:  "hitspec__hitspec_fetch",
		},
		{
			name:  "multi source investigation",
			query: "analizar fuentes de codebases, databases y resultados de CLI",
			want:  "cortex__cortex_investigate",
		},
		{
			name:  "feature implementation",
			query: "planear y desarrollar un feature en este repositorio",
			want:  "bob__bob_plan",
		},
		{
			name:  "spanish planning phase",
			query: "planificar el cambio de este repositorio antes de editar",
			want:  "bob__bob_plan",
		},
		{
			name:  "full english sentence not exact substring",
			query: "Please fetch this website as bounded Markdown text for research",
			want:  "hitspec__hitspec_fetch",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := rankCatalog(test.query, routingFixture())
			if len(got) == 0 {
				t.Fatalf("rankCatalog(%q) returned no matches", test.query)
			}
			if got[0].Namespaced != test.want {
				t.Fatalf("rankCatalog(%q) top = %s (score=%d terms=%v), want %s; all=%+v",
					test.query, got[0].Namespaced, got[0].Score, got[0].MatchedTerms, test.want, got)
			}
			if got[0].Score <= 0 || len(got[0].MatchedTerms) == 0 {
				t.Fatalf("top match lacks routing evidence: %+v", got[0])
			}
		})
	}
}

func TestRankCatalogRoutesBoundedMediaFacets(t *testing.T) {
	entries := []toolMatch{
		{
			Namespaced: "vidtrace__analyze_video", Server: "vidtrace", Tool: "analyze_video",
			Description: "Analyze a local video and return bounded scene and timeline evidence.",
			UseWhen:     []string{"inspect an external video file without copying it into the workspace"},
		},
		{
			Namespaced: "vision__inspect_image", Server: "vision", Tool: "inspect_image",
			Description: "Inspect a local image or screenshot.",
			UseWhen:     []string{"read an external image file"},
		},
	}
	for _, test := range []struct {
		query string
		want  string
	}{
		{"Available inputs: external_file, video. Analyze visual timeline evidence.", "vidtrace__analyze_video"},
		{"Available inputs: external_file, image. Inspect a screenshot.", "vision__inspect_image"},
	} {
		ranked := rankCatalog(test.query, entries)
		if len(ranked) == 0 || ranked[0].Namespaced != test.want {
			t.Fatalf("media route for %q = %+v, want %s", test.query, ranked, test.want)
		}
	}
}

func TestToolUseWhenDisambiguatesToolsOnOneServer(t *testing.T) {
	common := toolMatch{
		Server:      "vidtrace",
		Description: "Inspect local media.",
		UseWhen:     []string{"analyze an external media file"},
	}
	video := common
	video.Namespaced, video.Tool = "vidtrace__timeline", "timeline"
	video.ToolUseWhen = []string{"analyze video scenes and timeline transitions"}
	image := common
	image.Namespaced, image.Tool = "vidtrace__frame", "frame"
	image.ToolUseWhen = []string{"inspect one image or extracted video frame"}

	ranked := rankCatalog("analyze video timeline transitions", []toolMatch{image, video})
	if len(ranked) != 2 || ranked[0].Namespaced != video.Namespaced || ranked[0].Score <= ranked[1].Score {
		t.Fatalf("tool-specific routing = %+v, want video timeline first", ranked)
	}
}

func TestRankCatalogDeterminismAndNoMatch(t *testing.T) {
	entries := routingFixture()
	got := rankCatalog("fetch webpage", entries)
	if len(got) == 0 || got[0].Namespaced != "hitspec__hitspec_fetch" {
		t.Fatalf("exact tool query did not win: %+v", got)
	}
	if got := rankCatalog("quantum entanglement", entries); len(got) != 0 {
		t.Fatalf("unrelated query returned matches: %+v", got)
	}
	empty := rankCatalog("", []toolMatch{{Namespaced: "z__b"}, {Namespaced: "a__c"}})
	if names := []string{empty[0].Namespaced, empty[1].Namespaced}; !reflect.DeepEqual(names, []string{"a__c", "z__b"}) {
		t.Fatalf("empty-query catalog order = %v", names)
	}
	tied := rankCatalog("inspect HTTP", []toolMatch{
		{Namespaced: "z__tool", Tool: "tool", Description: "Inspect HTTP."},
		{Namespaced: "a__tool", Tool: "tool", Description: "Inspect HTTP."},
	})
	if len(tied) != 2 || tied[0].Score != tied[1].Score || tied[0].Namespaced != "a__tool" {
		t.Fatalf("equal scores are not deterministically namespaced: %+v", tied)
	}
}

func TestAssessRouteRequiresCoverageAndMargin(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		matches []toolMatch
		status  string
		reason  string
	}{
		{
			name:  "strong single candidate",
			query: "fetch webpage markdown",
			matches: []toolMatch{{
				Namespaced: "hitspec__fetch", Score: 52,
				MatchedTerms: []string{"web", "markdown"},
			}},
			status: "confident",
			reason: "single_candidate",
		},
		{
			name:  "weak incidental overlap",
			query: "complete a complex workspace task with a verified result",
			matches: []toolMatch{{
				Namespaced: "misc__task", Score: 9,
				MatchedTerms: []string{"task"},
			}},
			status: "ambiguous",
			reason: "weak_coverage",
		},
		{
			name:  "near tie",
			query: "inspect codebase",
			matches: []toolMatch{
				{Namespaced: "code__one", Score: 31, MatchedTerms: []string{"read", "codebase"}},
				{Namespaced: "code__two", Score: 29, MatchedTerms: []string{"read", "codebase"}},
			},
			status: "ambiguous",
			reason: "close_scores",
		},
		{
			name:  "long query with a misleading clear margin",
			query: "implementation phase in mcphub code format compile agent pin override tool definition budget changes focused tests without changing config",
			matches: []toolMatch{
				{Namespaced: "minerva__profile_create", Score: 78, MatchedTerms: []string{"build", "agent"}},
				{Namespaced: "cortex__note", Score: 65, MatchedTerms: []string{"phase", "code", "agent", "chang"}},
			},
			status: "ambiguous",
			reason: "low_matched_fraction",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := assessRoute(test.query, test.matches)
			if got.Status != test.status || !slices.Contains(got.ReasonCodes, test.reason) {
				t.Fatalf("assessment = %+v, want status=%s reason=%s", got, test.status, test.reason)
			}
		})
	}
}

func TestResolveToolKeepsLongLowCoverageImplementationQueryAmbiguous(t *testing.T) {
	downstream := sdk.NewServer(&sdk.Implementation{Name: "minerva-fixture", Version: "1"}, nil)
	downstream.AddTool(&sdk.Tool{
		Name:        "minerva_profile_create",
		Title:       "Create a new agent profile",
		Description: "Create a new agent profile with name, description, model, skills, MCP servers, and system prompt.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"name": map[string]any{"type": "string"}},
			"required":   []string{"name"},
		},
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{}, nil
	})
	downstream.AddTool(&sdk.Tool{
		Name:        "minerva_analytics",
		Title:       "View usage analytics",
		Description: "Return local skill activation and profile usage analytics.",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{}, nil
	})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return downstream }, nil)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	cfg := &config.Config{Servers: map[string]config.Server{
		"minerva": {
			URL:         httpServer.URL,
			Transport:   "http",
			Enabled:     true,
			Description: "Agent self-improvement, profiles, prompts, and local usage analytics",
			Tags:        []string{"agent", "profiles"},
			UseWhen:     []string{"manage agent profiles, skills, and system prompts"},
		},
	}}
	h := hub.New(cfg, nil, nil)
	h.Connect(context.Background())
	t.Cleanup(func() { _ = h.Close() })
	s := &Server{hub: h, cfg: cfg}

	query := "Implement and verify context-budget savings for small local Ollama models in local-agent and mcphub, including TUI cancellation behavior, lazy tool admission, prompt-aware ICE, and regression tests"
	_, resolvedAny, err := s.handleResolveTool(context.Background(), nil, resolveToolInput{Query: query})
	if err != nil {
		t.Fatal(err)
	}
	resolved := resolvedAny.(map[string]any)
	if resolved["status"] != "ambiguous" || resolved["ambiguous"] != true || resolved["confidence"] != "low" {
		t.Fatalf("low-coverage implementation query was treated as executable: %#v", resolved)
	}
	reasons := resolved["reason_codes"].([]string)
	if !slices.Contains(reasons, "low_matched_fraction") {
		t.Fatalf("resolver reasons = %v, want low_matched_fraction", reasons)
	}
	if hint := resolved["hint"].(string); !strings.Contains(hint, "do not auto-run") {
		t.Fatalf("ambiguous resolver hint invites execution: %q", hint)
	}
}

func TestCatalogRevisionIsStableAndContentAddressed(t *testing.T) {
	entries := routingFixture()
	first := catalogRevision(entries)
	reversed := append([]toolMatch(nil), entries...)
	slices.Reverse(reversed)
	if got := catalogRevision(reversed); got != first {
		t.Fatalf("catalog revision depends on order: %q vs %q", first, got)
	}
	changed := append([]toolMatch(nil), entries...)
	changed[0].UseWhen = append([]string(nil), changed[0].UseWhen...)
	changed[0].UseWhen[0] += " safely"
	if got := catalogRevision(changed); got == first {
		t.Fatalf("catalog revision did not change after routing metadata update: %q", got)
	}
}

func TestDiscoveryBoundsQueriesTermsAndMatchMetadata(t *testing.T) {
	var query strings.Builder
	for i := 0; i < maxIntentTerms+20; i++ {
		fmt.Fprintf(&query, "term%d ", i)
	}
	if terms := intentTerms(query.String()); len(terms) != maxIntentTerms {
		t.Fatalf("intent terms = %d, want capped %d", len(terms), maxIntentTerms)
	}
	entry := toolMatch{
		Namespaced:        "large__tool",
		Server:            "large",
		Tool:              "tool",
		Title:             strings.Repeat("title ", 100),
		Description:       strings.Repeat("description ", 200),
		ServerDescription: strings.Repeat("server ", 200),
		Tags:              []string{strings.Repeat("tag", 100), "two", "three", "four", "five", "six", "seven"},
		UseWhen:           []string{strings.Repeat("hint ", 100), "two", "three", "four"},
		MatchedTerms:      strings.Fields(strings.Repeat("term ", 40)),
	}
	compact := compactToolMatch(entry)
	if !compact.MetadataTruncated || len(compact.Title) > maxMatchTitle || len(compact.Description) > maxMatchDescription ||
		len(compact.ServerDescription) > maxMatchServerDesc || len(compact.Tags) > maxMatchTags ||
		len(compact.UseWhen) > maxMatchUseWhen || len(compact.MatchedTerms) > maxMatchTerms {
		t.Fatalf("match was not compacted within bounds: %+v", compact)
	}

	matches := make([]toolMatch, 100)
	for i := range matches {
		matches[i] = entry
		matches[i].Namespaced = fmt.Sprintf("large__tool_%03d", i)
	}
	returned, byteLimited := compactToolMatches(matches, len(matches))
	if !byteLimited || len(returned) >= len(matches) {
		t.Fatalf("byte bound did not limit matches: returned=%d limited=%t", len(returned), byteLimited)
	}
	encoded, err := json.Marshal(returned)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > maxCatalogMatchesBytes+2 {
		t.Fatalf("compact matches JSON = %d bytes, budget %d", len(encoded), maxCatalogMatchesBytes)
	}
}

func TestCatalogHandlersRejectOversizedQueryWithoutEcho(t *testing.T) {
	s := connectedCatalogServer(t, nil)
	query := strings.Repeat("x", maxDiscoveryQueryBytes) + "secret-tail"
	for name, call := range map[string]func() (any, error){
		"search": func() (any, error) {
			_, out, err := s.handleSearchTools(context.Background(), nil, searchInput{Query: query})
			return out, err
		},
		"resolve": func() (any, error) {
			_, out, err := s.handleResolveTool(context.Background(), nil, resolveToolInput{Query: query})
			return out, err
		},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := call()
			if err != nil {
				t.Fatal(err)
			}
			envelope := out.(map[string]any)
			if !strings.Contains(envelope["error"].(string), "query exceeds") {
				t.Fatalf("oversized query result = %#v", envelope)
			}
			if _, echoed := envelope["query"]; echoed {
				t.Fatalf("oversized query was echoed: %#v", envelope)
			}
		})
	}
}

func TestCapabilitySummaryRespectsScopeAndUseWhen(t *testing.T) {
	cfg := &config.Config{Expose: config.ExposeLazy, Servers: map[string]config.Server{
		"hitspec": {Enabled: true, Description: "fallback", UseWhen: []string{"capture web research"}},
		"cortex":  {Enabled: true, Description: "investigate evidence"},
		"off":     {Enabled: false, Description: "must not appear"},
	}}
	scope := &agentScope{servers: map[string]bool{"hitspec": true}}
	got := capabilitySummary(cfg, scope)
	if !strings.Contains(got, "hitspec: capture web research") {
		t.Fatalf("summary missing use_when: %q", got)
	}
	if strings.Contains(got, "cortex") || strings.Contains(got, "off") {
		t.Fatalf("summary leaked out-of-scope/disabled server: %q", got)
	}
}

func TestCapabilitySummaryRespectsExactAndEmptyToolScopes(t *testing.T) {
	cfg := &config.Config{Expose: config.ExposeLazy, Servers: map[string]config.Server{
		"hitspec": {Enabled: true, UseWhen: []string{"capture every kind of web research"}},
	}}
	exact := &agentScope{
		servers: map[string]bool{"hitspec": true},
		tools:   map[string]bool{"hitspec__hitspec_fetch": true},
	}
	got := capabilitySummary(cfg, exact)
	if !strings.Contains(got, "hitspec: allowed tools: hitspec_fetch") {
		t.Fatalf("exact tool summary = %q", got)
	}
	if strings.Contains(got, "every kind") {
		t.Fatalf("exact tool summary leaked server-wide routing hint: %q", got)
	}

	empty := &agentScope{servers: map[string]bool{"hitspec": true}, tools: map[string]bool{}}
	if got := capabilitySummary(cfg, empty); got != "" {
		t.Fatalf("empty tool scope summary = %q, want empty", got)
	}
}

func TestAllowedToolsHintCountsTheToolThatDoesNotFit(t *testing.T) {
	hint := allowedToolsHint([]string{
		strings.Repeat("a", 110),
		strings.Repeat("b", 110),
		strings.Repeat("c", 110),
	})
	if !strings.Contains(hint, "(+1 more)") {
		t.Fatalf("overflow hint undercounted omitted tools: %q", hint)
	}
	if len(hint) > maxCapabilityHintBytes {
		t.Fatalf("overflow hint = %d bytes, max %d", len(hint), maxCapabilityHintBytes)
	}
}

func TestCapabilitySummaryIsBoundedAndUTF8Safe(t *testing.T) {
	servers := map[string]config.Server{}
	for i := 0; i < 40; i++ {
		name := fmt.Sprintf("server-%02d", i)
		servers[name] = config.Server{
			Enabled: true,
			UseWhen: []string{strings.Repeat("página y evidencia ", 20)},
		}
	}
	got := capabilitySummary(&config.Config{Servers: servers}, nil)
	if len(got) > maxCapabilitySummaryBytes {
		t.Fatalf("summary is %d bytes, max %d", len(got), maxCapabilitySummaryBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("summary is not valid UTF-8: %q", got)
	}
	if !strings.Contains(got, "more") {
		t.Fatalf("bounded summary does not disclose omitted servers: %q", got)
	}
}

func connectedCatalogServer(t *testing.T, scope *agentScope) *Server {
	t.Helper()
	downstream := sdk.NewServer(&sdk.Implementation{Name: "catalog-fixture", Version: "1"}, nil)
	downstream.AddTool(&sdk.Tool{
		Name:        "hitspec_fetch",
		Title:       "Fetch one HTTP response",
		Description: "Fetch one direct HTTP URL or one saved request and return bounded raw, text, Markdown, or JSON without writing files.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"body":      map[string]any{"type": "string"},
				"file":      map[string]any{"type": "string"},
				"format":    map[string]any{"type": "string"},
				"headers":   map[string]any{"type": "object"},
				"method":    map[string]any{"type": "string"},
				"no_follow": map[string]any{"type": "boolean"},
				"url":       map[string]any{"type": "string"},
			},
		},
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{}, nil
	})
	for i := 0; i < 24; i++ {
		name := fmt.Sprintf("http_tool_%02d", i)
		downstream.AddTool(&sdk.Tool{Name: name, Description: "Inspect an HTTP workflow.", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{}, nil
		})
	}
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return downstream }, nil)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	cfg := &config.Config{Servers: map[string]config.Server{
		"hitspec": {
			URL:         httpServer.URL,
			Transport:   "http",
			Enabled:     true,
			Description: "Bounded HTTP fetches and saved-request validation",
			Tags:        []string{"web", "markdown"},
			UseWhen:     []string{"fetch a public HTTP URL as raw, text, Markdown, or JSON"},
		},
	}}
	h := hub.New(cfg, nil, nil)
	h.Connect(context.Background())
	t.Cleanup(func() { _ = h.Close() })
	return &Server{hub: h, cfg: cfg, scope: scope}
}

func TestCatalogHandlersRouteAndBoundResults(t *testing.T) {
	s := connectedCatalogServer(t, nil)

	_, resolvedAny, err := s.handleResolveTool(context.Background(), nil, resolveToolInput{
		Query: "investigar una URL y obtener Markdown",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved := resolvedAny.(map[string]any)
	if resolved["contract_version"] != contextualRoutingContractVersion || resolved["catalog_revision"] == "" || resolved["status"] != "confident" {
		t.Fatalf("versioned resolver control fields = %#v", resolved)
	}
	recommendation := resolved["recommendation"].(map[string]any)
	if recommendation["namespaced"] != "hitspec__hitspec_fetch" {
		t.Fatalf("recommendation = %#v", recommendation)
	}
	if recommendation["title"] != "Fetch one HTTP response" {
		t.Fatalf("recommendation title = %#v", recommendation["title"])
	}
	if !reflect.DeepEqual(recommendation["required_fields"], []string{}) {
		t.Fatalf("required_fields = %#v", recommendation["required_fields"])
	}
	template := recommendation["argument_template"].(map[string]any)
	if _, hasURL := template["url"]; !hasURL {
		t.Fatalf("argument template is missing url: %#v", template)
	}
	if _, hasFile := template["file"]; !hasFile {
		t.Fatalf("argument template is missing file: %#v", template)
	}
	if recommendation["argument_template_truncated"] != false {
		t.Fatalf("small argument template marked truncated: %#v", recommendation)
	}
	_, describedAny, err := s.handleDescribeTool(context.Background(), nil, describeInput{Tool: recommendation["namespaced"].(string)})
	if err != nil {
		t.Fatal(err)
	}
	if describedAny.(map[string]any)["input_schema"] == nil {
		t.Fatalf("describe did not return an input schema: %#v", describedAny)
	}
	if _, _, err := s.handleCallTool(context.Background(), nil, callInput{
		Tool:      recommendation["namespaced"].(string),
		Arguments: map[string]any{"url": "https://example.com"},
	}); err != nil {
		t.Fatalf("lazy resolve → describe → call failed: %v", err)
	}

	_, searchedAny, err := s.handleSearchTools(context.Background(), nil, searchInput{Query: "HTTP", MaxHits: 5})
	if err != nil {
		t.Fatal(err)
	}
	searched := searchedAny.(map[string]any)
	if searched["count"] != 25 || searched["returned"] != 5 || searched["truncated"] != true {
		t.Fatalf("bounded search output = %#v", searched)
	}

	_, ambiguousAny, err := s.handleResolveTool(context.Background(), nil, resolveToolInput{Query: "inspect workflow"})
	if err != nil {
		t.Fatal(err)
	}
	ambiguous := ambiguousAny.(map[string]any)
	if ambiguous["ambiguous"] != true {
		t.Fatalf("equal top scores did not set ambiguous: %#v", ambiguous)
	}
}

func TestCatalogHandlersRespectToolScope(t *testing.T) {
	s := connectedCatalogServer(t, &agentScope{
		servers: map[string]bool{"hitspec": true},
		tools:   map[string]bool{"hitspec__hitspec_fetch": true},
	})
	_, outAny, err := s.handleSearchTools(context.Background(), nil, searchInput{})
	if err != nil {
		t.Fatal(err)
	}
	out := outAny.(map[string]any)
	if out["count"] != 1 || out["returned"] != 1 {
		t.Fatalf("scoped search output = %#v", out)
	}
	matches := out["matches"].([]toolMatch)
	if len(matches) != 1 || matches[0].Namespaced != "hitspec__hitspec_fetch" {
		t.Fatalf("scoped matches = %#v", matches)
	}
}

func TestListServersCountsAndPinsRespectToolScope(t *testing.T) {
	s := connectedCatalogServer(t, &agentScope{
		servers: map[string]bool{"hitspec": true},
		tools:   map[string]bool{"hitspec__hitspec_fetch": true},
	})
	s.cfg.Pin = []string{"hitspec"}
	_, outAny, err := s.handleListServers(context.Background(), nil, emptyInput{})
	if err != nil {
		t.Fatal(err)
	}
	out := outAny.(map[string]any)
	if out["total_tools"] != 1 {
		t.Fatalf("scoped total_tools = %#v, want 1", out["total_tools"])
	}
	servers := out["servers"].([]serverInfo)
	if len(servers) != 1 || servers[0].Tools != 1 {
		t.Fatalf("scoped server counts = %#v", servers)
	}
	if pins := out["pinned"].([]string); !reflect.DeepEqual(pins, []string{"hitspec__hitspec_fetch"}) {
		t.Fatalf("scoped pins = %#v", pins)
	}

	s.scope.tools = map[string]bool{}
	_, emptyAny, err := s.handleListServers(context.Background(), nil, emptyInput{})
	if err != nil {
		t.Fatal(err)
	}
	empty := emptyAny.(map[string]any)
	if empty["total_tools"] != 0 || len(empty["pinned"].([]string)) != 0 || empty["servers"].([]serverInfo)[0].Tools != 0 {
		t.Fatalf("empty tool scope leaked counts or pins: %#v", empty)
	}
}

func TestLazyAgentPinOverrideRemovesGlobalPinsWithoutRestrictingCalls(t *testing.T) {
	fixture := connectedCatalogServer(t, nil)
	fixture.cfg.Expose = config.ExposeLazy
	fixture.cfg.Pin = []string{"hitspec"}
	noPins := []string{}
	scope := &agentScope{pin: &noPins}
	s := NewServer(fixture.cfg, fixture.hub, nil, scope)
	if err := s.mountDownstreamTools(); err != nil {
		t.Fatal(err)
	}

	client := connectServerClient(t, s.srv)
	list, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != managementToolCount {
		t.Fatalf("empty agent pin override advertised %d tools, want only %d management tools", len(list.Tools), managementToolCount)
	}
	for _, tool := range list.Tools {
		if strings.HasPrefix(tool.Name, "hitspec__") {
			t.Fatalf("global pin leaked through empty agent override: %s", tool.Name)
		}
	}

	// Pin policy affects tools/list only. The in-scope downstream tool remains
	// reachable through the lazy gateway call path.
	res, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "mcphub_call_tool",
		Arguments: json.RawMessage(`{"tool":"hitspec__hitspec_fetch"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unadvertised in-scope tool call failed: %s", textContent(res))
	}
}

func TestExposeAllIgnoresAgentPinOverride(t *testing.T) {
	fixture := connectedCatalogServer(t, nil)
	fixture.cfg.Expose = config.ExposeAll
	noPins := []string{}
	s := NewServer(fixture.cfg, fixture.hub, nil, &agentScope{pin: &noPins})
	if err := s.mountDownstreamTools(); err != nil {
		t.Fatal(err)
	}
	client := connectServerClient(t, s.srv)
	list, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(list.Tools), managementToolCount+25; got != want {
		t.Fatalf("expose all with empty pin override advertised %d tools, want %d", got, want)
	}
}

func TestToolSchemaBudgetKeepsManagementToolsAndReportsSavings(t *testing.T) {
	fixture := connectedCatalogServer(t, nil)
	fixture.cfg.Expose = config.ExposeLazy
	fixture.cfg.Pin = []string{"hitspec"}
	zero := 0
	s := NewServer(fixture.cfg, fixture.hub, nil, &agentScope{toolSchemaBudget: &zero})
	if err := s.mountDownstreamTools(); err != nil {
		t.Fatal(err)
	}

	client := connectServerClient(t, s.srv)
	list, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != managementToolCount {
		t.Fatalf("zero schema budget advertised %d tools, want %d management tools", len(list.Tools), managementToolCount)
	}
	if s.toolMountReport == nil ||
		s.toolMountReport.EligibleTools != 25 ||
		s.toolMountReport.AdvertisedTools != 0 ||
		s.toolMountReport.OmittedTools != 25 ||
		s.toolMountReport.EligibleDefinitionBytes == 0 {
		t.Fatalf("schema budget report = %+v", s.toolMountReport)
	}

	_, outAny, err := s.handleListServers(context.Background(), nil, emptyInput{})
	if err != nil {
		t.Fatal(err)
	}
	out := outAny.(map[string]any)
	report, ok := out["tool_schema_budget"].(hub.ToolMountReport)
	if !ok || report.EligibleTools != 25 || report.AdvertisedTools != 0 {
		t.Fatalf("list_servers budget diagnostic = %#v", out["tool_schema_budget"])
	}
	if out["management_tools"] != managementToolCount || out["advertised_downstream_tools"] != 0 {
		t.Fatalf("list_servers surface diagnostic = %#v", out)
	}
}

func TestDescribeToolAcceptsStutterCollapsedAlias(t *testing.T) {
	// Downstream servers self-prefix their tool names (hitspec_fetch on the
	// hitspec server), so the namespaced form stutters
	// (hitspec__hitspec_fetch). Callers reasonably try the collapsed
	// hitspec__fetch or {server: hitspec, tool: fetch} and previously got
	// "tool not found"; both must resolve to the canonical downstream name.
	s := connectedCatalogServer(t, nil)
	for _, in := range []describeInput{
		{Tool: "hitspec__fetch"},
		{Server: "hitspec", Tool: "fetch"},
		{Server: "hitspec", Tool: "hitspec_fetch"}, // exact split form
		{Tool: "hitspec__hitspec_fetch"},           // exact combined form
	} {
		_, describedAny, err := s.handleDescribeTool(context.Background(), nil, in)
		if err != nil {
			t.Fatalf("describe %+v: %v", in, err)
		}
		described := describedAny.(map[string]any)
		if described["error"] != nil {
			t.Fatalf("describe %+v returned error: %#v", in, described)
		}
		if described["tool"] != "hitspec_fetch" || described["namespaced"] != "hitspec__hitspec_fetch" {
			t.Errorf("describe %+v should report the canonical name, got %#v", in, described)
		}
	}
	// A name that matches nothing even after prefixing still misses.
	_, missAny, err := s.handleDescribeTool(context.Background(), nil, describeInput{Tool: "hitspec__nope"})
	if err != nil {
		t.Fatal(err)
	}
	if missAny.(map[string]any)["error"] != "tool not found" {
		t.Errorf("unknown alias should still be not found, got %#v", missAny)
	}
}
