package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
)

// resetFlags restores the package-global persistent-flag state so each test
// runs independently of the others.
func resetFlags() {
	flagConfig, flagDB, flagJSON = "", "", false
}

// runRoot executes the mcphub root command tree with the given args, capturing
// combined stdout/stderr. The config/db/json globals are reset first.
func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetFlags()
	root := Root()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

// writeConfig writes a minimal valid mcphub.yaml to a temp path and returns it.
func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "mcphub.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const minimalConfig = "version: 1\nservers: {}\nagents: {}\n"

// TestAddEnabledNoOp asserts --enabled is accepted (server stays enabled, the
// default) and is mutually exclusive with --disabled.
func TestAddEnabledNoOp(t *testing.T) {
	dir := t.TempDir()
	cfg := writeConfig(t, dir, minimalConfig)

	// --enabled: accepted, server added enabled.
	if _, err := runRoot(t, "add", "foo", "echo", "hi", "--enabled", "--config", cfg); err != nil {
		t.Fatalf("--enabled rejected: %v", err)
	}
	c, err := config.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Servers["foo"].Enabled {
		t.Errorf("--enabled: server foo not enabled (got %v)", c.Servers["foo"].Enabled)
	}

	// Neither flag: enabled is the default.
	dir2 := t.TempDir()
	cfg2 := writeConfig(t, dir2, minimalConfig)
	if _, err := runRoot(t, "add", "bar", "echo", "--config", cfg2); err != nil {
		t.Fatalf("bare add failed: %v", err)
	}
	c2, _ := config.Load(cfg2)
	if !c2.Servers["bar"].Enabled {
		t.Errorf("bare add: server bar not enabled by default (got %v)", c2.Servers["bar"].Enabled)
	}

	// --disabled: server added disabled.
	dir3 := t.TempDir()
	cfg3 := writeConfig(t, dir3, minimalConfig)
	if _, err := runRoot(t, "add", "baz", "echo", "--disabled", "--config", cfg3); err != nil {
		t.Fatalf("--disabled failed: %v", err)
	}
	c3, _ := config.Load(cfg3)
	if c3.Servers["baz"].Enabled {
		t.Errorf("--disabled: server baz enabled, want disabled")
	}

	// --enabled --disabled: mutually exclusive → error.
	dir4 := t.TempDir()
	cfg4 := writeConfig(t, dir4, minimalConfig)
	_, err = runRoot(t, "add", "qux", "echo", "--enabled", "--disabled", "--config", cfg4)
	if err == nil {
		t.Errorf("--enabled --disabled: expected error, got nil")
	}
}
