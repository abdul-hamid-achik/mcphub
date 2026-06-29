package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
)

func TestRenderStatsMarkdown(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	totals := store.Totals{Calls: 5, Errors: 1, EstTokens: 100, TotalMs: 250}
	servers := []store.ServerStat{{Server: "codemap", Calls: 5, Errors: 1, AvgMs: 50, EstTokens: 100}}
	if err := renderStatsMarkdown(cmd, "last 24h", totals, servers, nil, false); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for _, want := range []string{"# mcphub stats", "Totals (last 24h)", "| Server |", "| codemap |"} {
		if !strings.Contains(s, want) {
			t.Errorf("markdown missing %q:\n%s", want, s)
		}
	}
}

func TestRenderStatusMarkdown(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	rep := statusReport{
		Config: "/x/mcphub.yaml", Expose: "lazy", Servers: 3, Enabled: 2,
		Agents: []agentStatus{{Agent: "claude", Type: "claude", Mode: "gateway", State: "in sync"}},
		Calls:  10, Unused: []string{"glyph"},
	}
	if err := renderStatusMarkdown(cmd, rep); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for _, want := range []string{"# mcphub status", "| Agent |", "| claude |", "**Unused**", "glyph"} {
		if !strings.Contains(s, want) {
			t.Errorf("markdown missing %q:\n%s", want, s)
		}
	}
}

func TestParseSince(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", 0, false},
		{"24h", 24 * time.Hour, false},
		{"90m", 90 * time.Minute, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{" 2h ", 2 * time.Hour, false},
		{"bogus", 0, true},
		{"-5h", 0, true},
		{"3w", 0, true},
	}
	for _, c := range cases {
		got, err := parseSince(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseSince(%q) expected error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSince(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("parseSince(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestStarterConfigMatchesStarterStruct asserts that the YAML starter string
// in commands.go and the structured Starter() in config.go define the same
// set of servers, groups, and agents — so the two sources of truth can't drift.
func TestStarterConfigMatchesStarterStruct(t *testing.T) {
	var fromYAML config.Config
	if err := yaml.Unmarshal([]byte(starterConfig), &fromYAML); err != nil {
		t.Fatalf("parse starterConfig YAML: %v", err)
	}
	want := config.Starter()

	if fromYAML.Version != want.Version {
		t.Errorf("version: yaml %d vs struct %d", fromYAML.Version, want.Version)
	}
	if fromYAML.Expose != want.Expose {
		t.Errorf("expose: yaml %q vs struct %q", fromYAML.Expose, want.Expose)
	}
	if !reflect.DeepEqual(fromYAML.Servers, want.Servers) {
		t.Errorf("servers differ:\nyaml  = %+v\nstruct = %+v", fromYAML.Servers, want.Servers)
	}
	if !reflect.DeepEqual(fromYAML.Groups, want.Groups) {
		t.Errorf("groups differ:\nyaml  = %+v\nstruct = %+v", fromYAML.Groups, want.Groups)
	}
	if !reflect.DeepEqual(fromYAML.Agents, want.Agents) {
		t.Errorf("agents differ:\nyaml  = %+v\nstruct = %+v", fromYAML.Agents, want.Agents)
	}
}

func TestParseEnv(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		want    map[string]string
		wantErr bool
	}{
		{"empty", nil, nil, false},
		{"single", []string{"KEY=val"}, map[string]string{"KEY": "val"}, false},
		{"multiple", []string{"A=1", "B=2"}, map[string]string{"A": "1", "B": "2"}, false},
		{"value with equals", []string{"TOKEN=a=b=c"}, map[string]string{"TOKEN": "a=b=c"}, false},
		{"empty value", []string{"KEY="}, map[string]string{"KEY": ""}, false},
		{"missing equals", []string{"NOSEP"}, nil, true},
		{"empty key", []string{"=val"}, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseEnv(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parseEnv(%v) expected error, got %v", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseEnv(%v) unexpected error: %v", c.in, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseEnv(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestFilterServerStats(t *testing.T) {
	rows := []store.ServerStat{
		{Server: "codemap", Calls: 10},
		{Server: "vecgrep", Calls: 5},
		{Server: "monitor", Calls: 2},
	}
	got := filterServerStats(rows, "codemap")
	if len(got) != 1 || got[0].Server != "codemap" {
		t.Fatalf("filterServerStats = %+v, want only codemap", got)
	}
	if got := filterServerStats(rows, "ghost"); len(got) != 0 {
		t.Errorf("filterServerStats(ghost) = %d rows, want 0", len(got))
	}
}

func TestFilterToolStats(t *testing.T) {
	rows := []store.ToolStat{
		{Server: "codemap", Tool: "search"},
		{Server: "codemap", Tool: "find"},
		{Server: "vecgrep", Tool: "vecgrep_search"},
	}
	got := filterToolStats(rows, "codemap")
	if len(got) != 2 {
		t.Fatalf("filterToolStats = %d rows, want 2", len(got))
	}
}

func TestFilterRecentCalls(t *testing.T) {
	rows := []recentCall{
		{Namespaced: "codemap__search"},
		{Namespaced: "vecgrep__vecgrep_search"},
		{Namespaced: "monitor__procs"},
	}
	got := filterRecentCalls(rows, "codemap")
	if len(got) != 1 || got[0].Namespaced != "codemap__search" {
		t.Fatalf("filterRecentCalls = %+v, want only codemap__search", got)
	}
}
