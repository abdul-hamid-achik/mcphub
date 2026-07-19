package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestRootDefersErrorEmissionToExecute(t *testing.T) {
	resetFlags()
	root := Root()
	if !root.SilenceErrors {
		t.Fatal("Root.SilenceErrors = false; Execute would duplicate Cobra's error")
	}

	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"definitely-not-a-command"})
	err := root.Execute()
	if err == nil {
		t.Fatal("unknown command returned nil error")
	}
	if stderr.Len() != 0 {
		t.Fatalf("Cobra emitted an error before Execute's single emitter: %q", stderr.String())
	}

	// Mirror Execute's sole emission without invoking os.Exit in-process.
	fmt.Fprintln(&stderr, "Error:", err)
	if got := strings.Count(stderr.String(), "Error:"); got != 1 {
		t.Fatalf("error emitted %d times, want exactly once: %q", got, stderr.String())
	}
}
