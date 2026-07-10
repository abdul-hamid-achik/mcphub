// Package runtimepath makes GUI-launched MCPHub processes see the standard
// user install locations that interactive login shells normally add to PATH.
package runtimepath

import (
	"os"
	"path/filepath"
	"strings"
)

// Apply augments PATH once, preserving the inherited order and appending only
// existing standard install directories. Appending avoids allowing a
// user-writable directory to shadow an inherited system executable.
func Apply() error {
	executable, _ := os.Executable()
	home, _ := os.UserHomeDir()
	return os.Setenv("PATH", Augment(os.Getenv("PATH"), home, executable))
}

// Augment is the pure path-building operation used by Apply and tests.
func Augment(current, home, executable string) string {
	parts := filepath.SplitList(current)
	candidates := []string{
		filepath.Dir(executable),
		"/opt/homebrew/bin",
		"/usr/local/bin",
	}
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".bun", "bin"),
		)
	}

	seen := make(map[string]struct{}, len(parts)+len(candidates))
	result := make([]string, 0, len(parts)+len(candidates))
	appendPath := func(path string, requireDirectory bool) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		if _, duplicate := seen[path]; duplicate {
			return
		}
		if requireDirectory {
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() {
				return
			}
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}

	for _, path := range parts {
		appendPath(path, false)
	}
	for _, path := range candidates {
		appendPath(path, true)
	}
	return strings.Join(result, string(os.PathListSeparator))
}
