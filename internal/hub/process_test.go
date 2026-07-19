package hub

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
)

func TestServerEnvironmentIsolatesTinyVaultCredentials(t *testing.T) {
	inherited := []string{
		"PATH=/usr/bin",
		"HOME=/tmp/home",
		"APP_MODE=ambient",
		"TVAULT_PASSPHRASE=master-passphrase",
		"TVAULT_IDENTITY_KEY=private-identity",
		"TVAULT_AGENT_TOKEN=capability-token",
		"tvault_passphrase=lowercase-passphrase",
		"TVAULT_DIR=/tmp/vault",
		"TVAULT_NO_AGENT=1",
		"TAVILY_API_KEY=stale-ambient-key",
		"HITSPEC_SHARED=stale-prefix-value",
	}

	t.Run("ordinary downstream", func(t *testing.T) {
		srv := config.Server{
			Command: "hitspec",
			Env: map[string]string{
				"APP_MODE":          "explicit",
				"TVAULT_PASSPHRASE": "must-not-pass",
			},
		}
		got := environmentMap(serverEnvironment(srv, inherited))
		for _, name := range []string{"TVAULT_PASSPHRASE", "TVAULT_IDENTITY_KEY", "TVAULT_AGENT_TOKEN", "tvault_passphrase"} {
			if _, ok := got[name]; ok {
				t.Errorf("%s reached an ordinary downstream", name)
			}
		}
		if got["APP_MODE"] != "explicit" {
			t.Errorf("explicit env override = %q, want explicit", got["APP_MODE"])
		}
		if got["PATH"] != "/usr/bin" {
			t.Errorf("PATH = %q, want inherited PATH", got["PATH"])
		}
	})

	t.Run("vault wrapper", func(t *testing.T) {
		srv := config.Server{
			Command:     "hitspec",
			Vault:       "research",
			VaultOnly:   []string{"TAVILY_API_KEY", "TVAULT_PASSPHRASE"},
			VaultPrefix: "HITSPEC_",
			Env: map[string]string{
				"APP_MODE":         "explicit",
				"TAVILY_API_KEY":   "stale-explicit-key",
				"HITSPEC_SHARED":   "stale-explicit-prefix",
				"UNRELATED_CONFIG": "keep",
			},
		}
		got := environmentMap(serverEnvironment(srv, inherited))
		for name, want := range map[string]string{
			"TVAULT_PASSPHRASE":   "master-passphrase",
			"TVAULT_IDENTITY_KEY": "private-identity",
			"TVAULT_AGENT_TOKEN":  "capability-token",
			"TVAULT_DIR":          "/tmp/vault",
			"TVAULT_NO_AGENT":     "1",
		} {
			if got[name] != want {
				t.Errorf("%s = %q, want preserved for tvault wrapper", name, got[name])
			}
		}
		for _, name := range []string{"TAVILY_API_KEY", "HITSPEC_SHARED"} {
			if _, ok := got[name]; ok {
				t.Errorf("selected vault variable %s reached wrapper from ambient/config env", name)
			}
		}
		if got["UNRELATED_CONFIG"] != "keep" {
			t.Errorf("unrelated explicit env = %q, want keep", got["UNRELATED_CONFIG"])
		}
	})

	t.Run("direct tvault server", func(t *testing.T) {
		srv := config.Server{Command: "/opt/homebrew/bin/tvault", Args: []string{"mcp"}}
		got := environmentMap(serverEnvironment(srv, inherited))
		for _, name := range []string{"TVAULT_PASSPHRASE", "TVAULT_IDENTITY_KEY", "TVAULT_AGENT_TOKEN"} {
			if _, ok := got[name]; !ok {
				t.Errorf("%s was removed from a directly configured tvault server", name)
			}
		}
	})
}

func TestBoundedDiagnosticBufferRetainsTail(t *testing.T) {
	b := &boundedDiagnosticBuffer{limit: 32}
	input := strings.Repeat("x", 128) + "useful-tail"
	if n, err := b.Write([]byte(input)); err != nil || n != len(input) {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if got := b.String(); len(got) != 32 || !strings.HasSuffix(got, "useful-tail") {
		t.Fatalf("buffer = %q (%d bytes), want 32-byte useful tail", got, len(got))
	}
	if !b.Truncated() {
		t.Fatal("Truncated = false, want true")
	}
}

func TestBoundedDiagnosticBufferExactLimitIsNotTruncated(t *testing.T) {
	t.Run("one write", func(t *testing.T) {
		b := &boundedDiagnosticBuffer{limit: 32}
		if _, err := b.Write([]byte(strings.Repeat("a", 32))); err != nil {
			t.Fatal(err)
		}
		if got := b.String(); len(got) != 32 {
			t.Fatalf("exact-limit buffer = %d bytes, want 32", len(got))
		}
		if b.Truncated() {
			t.Fatal("single exact-limit write incorrectly marked truncated")
		}
	})

	t.Run("multiple writes then overflow", func(t *testing.T) {
		b := &boundedDiagnosticBuffer{limit: 32}
		if _, err := b.Write([]byte(strings.Repeat("a", 12))); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Write([]byte(strings.Repeat("b", 20))); err != nil {
			t.Fatal(err)
		}
		if got := b.String(); len(got) != 32 {
			t.Fatalf("exact-limit buffer = %d bytes, want 32", len(got))
		}
		if b.Truncated() {
			t.Fatal("accumulated exact-limit buffer incorrectly marked truncated")
		}
		if _, err := b.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		if !b.Truncated() {
			t.Fatal("over-limit buffer was not marked truncated")
		}
	})
}

func TestSanitizeStartupDetailRedactsAndBounds(t *testing.T) {
	raw := "\x1b[31mTAVILY_API_KEY=visible-value\x1b[0m " +
		`{"access_token":"json-value"} Authorization: Basic basic-value Bearer bearer-value ` +
		"https://person:url-password@example.test/path " +
		"-----BEGIN PRIVATE KEY-----\nprivate-material\n-----END PRIVATE KEY----- " +
		strings.Repeat("z", maxStartupDetailBytes*2)
	got := sanitizeStartupDetail(raw, []string{"visible-value"}, nil, false)
	for _, secret := range []string{"visible-value", "json-value", "basic-value", "bearer-value", "url-password", "private-material"} {
		if strings.Contains(got, secret) {
			t.Errorf("sanitized detail leaked %q: %s", secret, got)
		}
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("sanitized detail retained ANSI escape: %q", got)
	}
	if len(got) > maxStartupDetailBytes {
		t.Errorf("sanitized detail = %d bytes, budget %d", len(got), maxStartupDetailBytes)
	}
	if got != "downstream emitted startup diagnostics; detail suppressed" {
		t.Errorf("unknown child stderr was not suppressed: %q", got)
	}
}

func TestSanitizeStartupDetailKeepsOnlySafeActionableSignals(t *testing.T) {
	safeNames := startupEnvironmentAllowlist(config.Server{
		Env:       map[string]string{"DECLARED_TOKEN": "value"},
		VaultOnly: []string{"TAVILY_API_KEY"},
	})
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "vault locked",
			raw:  "unlabelled-secret vault is locked; provide TVAULT_PASSPHRASE",
			want: "vault is locked; start a TinyVault agent or provide unlock credentials to the launcher",
		},
		{
			name: "required environment",
			raw:  "TAVILY_API_KEY is required for the tavily provider; unlabelled-secret",
			want: "required environment variable TAVILY_API_KEY is unavailable",
		},
		{
			name: "declared env key",
			raw:  "DECLARED_TOKEN required; unlabelled-secret",
			want: "required environment variable DECLARED_TOKEN is unavailable",
		},
		{
			name: "arbitrary uppercase identifier",
			raw:  "SUPERSECRET required",
			want: "a required environment variable is unavailable",
		},
		{
			name: "unknown stderr",
			raw:  "unlabelled-secret",
			want: "downstream emitted startup diagnostics; detail suppressed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeStartupDetail(tc.raw, nil, safeNames, false)
			if got != tc.want {
				t.Fatalf("sanitizeStartupDetail() = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "unlabelled-secret") || strings.Contains(got, "SUPERSECRET") {
				t.Fatalf("safe summary leaked unknown child content: %q", got)
			}
		})
	}
}

func TestConnectReportsSanitizedBoundedStderr(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fail-mcp")
	body := `#!/bin/sh
printf '\033[31mTAVILY_API_KEY=%s\033[0m\n' "$TAVILY_API_KEY" >&2
printf 'vault is locked; start the local agent\n' >&2
exit 3
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{
		Version:        1,
		ConnectTimeout: "2s",
		Servers: map[string]config.Server{
			"broken": {
				Command: script,
				Env:     map[string]string{"TAVILY_API_KEY": "super-secret-value"},
				Enabled: true,
			},
		},
		Agents: map[string]config.Agent{},
	}
	h := New(c, nil, nil)
	h.Connect(context.Background())
	defer h.Close()

	downstreams := h.Downstreams()
	if len(downstreams) != 1 || downstreams[0].Err == nil {
		t.Fatalf("downstreams = %+v, want one failed connection", downstreams)
	}
	got := downstreams[0].Err.Error()
	for _, want := range []string{"connect:", "downstream stderr:", "vault is locked", "TinyVault agent"} {
		if !strings.Contains(got, want) {
			t.Errorf("connection error missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "super-secret-value") || strings.Contains(got, "\x1b") {
		t.Errorf("connection error leaked sensitive/control content: %q", got)
	}
}

func environmentMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			out[name] = value
		}
	}
	return out
}
