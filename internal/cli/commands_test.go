package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

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
