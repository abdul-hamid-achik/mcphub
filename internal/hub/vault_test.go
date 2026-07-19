package hub

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
)

func TestParseVaultRef(t *testing.T) {
	cases := []struct {
		ref          string
		project, key string
		ok           bool
	}{
		{"tvault://obsidian/authorization", "obsidian", "authorization", true},
		{"tvault://default/api_key", "default", "api_key", true},
		{"tvault://API_KEY", "", "API_KEY", true},
		{"tvault://current/secret", "", "secret", true},
		{"tvault://obsidian/bearer%20token", "obsidian", "bearer%20token", true},
		{"tvault://", "", "", false},
		{"tvault:///key", "", "key", true},            // empty project → active
		{"tvault://obsidian/", "obsidian", "", false}, // empty key
	}
	for _, c := range cases {
		t.Run(c.ref, func(t *testing.T) {
			project, key, ok := parseVaultRef(c.ref)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if ok {
				if project != c.project {
					t.Errorf("project = %q, want %q", project, c.project)
				}
				if key != c.key {
					t.Errorf("key = %q, want %q", key, c.key)
				}
			}
		})
	}
}

func TestResolveVaultHeadersPassthrough(t *testing.T) {
	in := map[string]string{
		"Authorization": "Bearer literal-token",
		"X-Custom":      "plain-value",
		"X-No-Tvault":   "tvault is not in this value",
	}
	out, err := resolveVaultHeaders(context.Background(), in)
	if err != nil {
		t.Fatalf("resolveVaultHeaders: %v", err)
	}
	for k, v := range in {
		if out[k] != v {
			t.Errorf("header %q = %q, want %q (passthrough)", k, out[k], v)
		}
	}
}

func TestResolveVaultHeadersRejectsMalformed(t *testing.T) {
	cases := []string{
		"tvault://",
		"tvault://obsidian/",
	}
	for _, val := range cases {
		t.Run(val, func(t *testing.T) {
			_, err := resolveVaultHeaders(context.Background(), map[string]string{"Authorization": val})
			if err == nil {
				t.Fatalf("expected error for %q, got nil", val)
			}
		})
	}
}

func TestTVaultGetDiagnosticsAreFailClosed(t *testing.T) {
	installFakeTVault(t)
	for _, tc := range []struct {
		name string
		mode string
		want string
	}{
		{
			name: "vault locked",
			mode: "locked",
			want: "vault is locked; start a TinyVault agent or provide unlock credentials to the launcher",
		},
		{
			name: "unknown stderr",
			mode: "unknown",
			want: "downstream emitted startup diagnostics; detail suppressed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAKE_TVAULT_MODE", tc.mode)
			value, err := tvaultGet(context.Background(), "authorization", "obsidian")
			if err == nil {
				t.Fatalf("tvaultGet() = %q, nil; want error", value)
			}
			if value != "" {
				t.Fatalf("tvaultGet() returned secret value on error: %q", value)
			}
			got := err.Error()
			if !strings.Contains(got, tc.want) {
				t.Errorf("error missing safe diagnostic %q: %s", tc.want, got)
			}
			if strings.Contains(got, "unlabelled-vault-secret") {
				t.Errorf("error leaked arbitrary tvault stderr: %s", got)
			}
		})
	}
}

func TestTVaultGetBoundsSecretStdout(t *testing.T) {
	installFakeTVault(t)

	t.Run("exact limit succeeds", func(t *testing.T) {
		t.Setenv("FAKE_TVAULT_MODE", "exact")
		value, err := tvaultGet(context.Background(), "authorization", "")
		if err != nil {
			t.Fatalf("tvaultGet exact limit: %v", err)
		}
		if len(value) != maxTVaultSecretBytes {
			t.Fatalf("exact-limit value = %d bytes, want %d", len(value), maxTVaultSecretBytes)
		}
	})

	t.Run("over limit fails without value", func(t *testing.T) {
		t.Setenv("FAKE_TVAULT_MODE", "oversized")
		value, err := tvaultGet(context.Background(), "authorization", "")
		if err == nil {
			t.Fatalf("tvaultGet oversized = %d-byte value, nil; want error", len(value))
		}
		if value != "" {
			t.Fatalf("tvaultGet returned %d secret bytes after overflow", len(value))
		}
		if !strings.Contains(err.Error(), "secret output exceeded 65536-byte limit") {
			t.Errorf("oversized error is not fixed/actionable: %v", err)
		}
		if strings.Contains(err.Error(), strings.Repeat("0", 32)) {
			t.Errorf("oversized error leaked secret output: %v", err)
		}
	})
}

func TestTVaultGetHonorsContextWithoutLeakingStderr(t *testing.T) {
	installFakeTVault(t)
	t.Setenv("FAKE_TVAULT_MODE", "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	value, err := tvaultGet(ctx, "authorization", "")
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("tvaultGet cancellation error = %v, want context deadline exceeded", err)
	}
	if value != "" {
		t.Fatalf("tvaultGet returned value after cancellation: %q", value)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("tvaultGet ignored context for %v", elapsed)
	}
	if strings.Contains(err.Error(), "unlabelled-vault-secret") {
		t.Fatalf("cancellation error leaked child stderr: %v", err)
	}
}

func TestConnectRemoteVaultHeaderHonorsConnectTimeout(t *testing.T) {
	installFakeTVault(t)
	t.Setenv("FAKE_TVAULT_MODE", "hang")
	c := &config.Config{
		Version:        1,
		ConnectTimeout: "100ms",
		Servers: map[string]config.Server{
			"remote": {
				URL:     "https://127.0.0.1:1/mcp",
				Headers: map[string]string{"Authorization": "tvault://obsidian/authorization"},
				Enabled: true,
			},
		},
		Agents: map[string]config.Agent{},
	}
	h := New(c, nil, nil)
	start := time.Now()
	h.Connect(context.Background())
	elapsed := time.Since(start)
	defer h.Close()

	downstreams := h.Downstreams()
	if len(downstreams) != 1 || downstreams[0].Err == nil {
		t.Fatalf("downstreams = %+v, want one timeout", downstreams)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("remote vault resolution ignored connect timeout for %v", elapsed)
	}
	got := downstreams[0].Err.Error()
	if !strings.Contains(got, context.DeadlineExceeded.Error()) {
		t.Errorf("connect error = %q, want deadline exceeded", got)
	}
	if strings.Contains(got, "unlabelled-vault-secret") {
		t.Errorf("connect timeout leaked child stderr: %q", got)
	}
}

func installFakeTVault(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tvault")
	script := `#!/bin/sh
emit_limit() {
  i=0
  while [ "$i" -lt 1024 ]; do
    printf '%064d' 0
    i=$((i + 1))
  done
}

case "$FAKE_TVAULT_MODE" in
  locked)
    printf '%s\n' 'unlabelled-vault-secret vault is locked; start the local agent' >&2
    exit 3
    ;;
  unknown)
    printf '%s\n' 'unlabelled-vault-secret arbitrary failure detail' >&2
    exit 4
    ;;
  exact)
    emit_limit
    ;;
  oversized)
    emit_limit
    printf x
    ;;
  hang)
    printf '%s\n' 'unlabelled-vault-secret from hanging child' >&2
    while :; do :; done
    ;;
  *)
    printf '%s\n' 'resolved-header-secret'
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
