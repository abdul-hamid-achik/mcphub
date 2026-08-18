package mcp

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/hub"
)

func TestCanonicalManagementName(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"list_servers", "list_servers", true},
		{"mcphub_list_servers", "list_servers", true},
		{"mcphub__list_servers", "list_servers", true},
		{"mcphub__mcphub_list_servers", "list_servers", true},
		{"mcphub_resolve_tool", "resolve_tool", true},
		{"mcphub__mcphub_call_tool", "call_tool", true},
		{"stats", "stats", true},
		{"mcphub_stats", "stats", true},
		{"bob__plan", "bob__plan", false},
		{"mcphub_not_a_tool", "mcphub_not_a_tool", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := canonicalManagementName(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("canonicalManagementName(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestManagementToolsAdvertiseCleanNamesAndTitles(t *testing.T) {
	cfg := &config.Config{Expose: config.ExposeLazy}
	s := NewServer(cfg, hub.New(cfg, nil, nil), nil, nil)
	client := connectServerClient(t, s.srv)
	list, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != managementToolCount {
		t.Fatalf("advertised %d tools, want %d", len(list.Tools), managementToolCount)
	}

	want := map[string]string{}
	for _, tool := range managementTools {
		want[tool.Public] = tool.Title
	}
	for _, tool := range list.Tools {
		if strings.HasPrefix(tool.Name, "mcphub_") {
			t.Errorf("advertised self-prefixed management name %q", tool.Name)
		}
		title, ok := want[tool.Name]
		if !ok {
			t.Errorf("unexpected advertised tool %q", tool.Name)
			continue
		}
		if tool.Title != title {
			t.Errorf("%s title = %q, want %q", tool.Name, tool.Title, title)
		}
		delete(want, tool.Name)
	}
	for name := range want {
		t.Errorf("missing advertised tool %q", name)
	}
}

func TestManagementToolLegacyAliases(t *testing.T) {
	cfg := &config.Config{Expose: config.ExposeLazy}
	s := NewServer(cfg, hub.New(cfg, nil, nil), nil, nil)
	client := connectServerClient(t, s.srv)

	aliases := []string{
		"list_servers",
		"mcphub_list_servers",
		"mcphub__list_servers",
		"mcphub__mcphub_list_servers",
	}
	for _, name := range aliases {
		res, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: name})
		if err != nil {
			t.Fatalf("CallTool(%q): %v", name, err)
		}
		if res.IsError {
			t.Fatalf("CallTool(%q) isError: %s", name, textContent(res))
		}
		if !strings.Contains(textContent(res), `"servers"`) {
			t.Fatalf("CallTool(%q) missing servers payload:\n%s", name, textContent(res))
		}
	}

	_, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "mcphub_not_a_tool"})
	if err == nil {
		t.Fatal("unknown legacy-shaped name should not resolve")
	}
}

func TestLazyInstructionsUseCleanManagementNames(t *testing.T) {
	s, _ := serverWithResultStore(t, nil)
	client := connectServerClient(t, s.srv)
	instructions := client.InitializeResult().Instructions
	for _, banned := range []string{
		"mcphub_list_servers",
		"mcphub_resolve_tool",
		"mcphub_search_tools",
		"mcphub_call_tool",
		"mcphub_stats",
	} {
		if strings.Contains(instructions, banned) {
			t.Errorf("lazy instructions still mention %q:\n%s", banned, instructions)
		}
	}
	for _, required := range []string{
		"`list_servers`",
		"`resolve_tool`",
		"`search_tools`",
		"`call_tool`",
		"`stats`",
	} {
		if !strings.Contains(instructions, required) {
			t.Errorf("lazy instructions omit %q:\n%s", required, instructions)
		}
	}
}
