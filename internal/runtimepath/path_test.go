package runtimepath

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAugmentPreservesOrderAndAddsUserInstallDirs(t *testing.T) {
	home := t.TempDir()
	executableDir := filepath.Join(home, "app-bin")
	for _, dir := range []string{
		executableDir,
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "go", "bin"),
		filepath.Join(home, ".bun", "bin"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got := filepath.SplitList(Augment("/usr/bin:/bin:/usr/bin", home, filepath.Join(executableDir, "mcphub")))
	wantPrefix := []string{"/usr/bin", "/bin", executableDir}
	if !reflect.DeepEqual(got[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("PATH prefix = %#v, want %#v", got[:len(wantPrefix)], wantPrefix)
	}
	for _, want := range []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "go", "bin"),
		filepath.Join(home, ".bun", "bin"),
	} {
		if !contains(got, want) {
			t.Errorf("augmented PATH missing %q: %#v", want, got)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
