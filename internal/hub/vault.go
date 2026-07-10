package hub

import (
	"fmt"
	"os/exec"
	"strings"
)

// vaultScheme is the prefix that marks a header value as a tvault reference.
// Syntax: tvault://<project>/<key> or tvault://<key> (active project).
const vaultScheme = "tvault://"

// resolveVaultHeaders resolves any header values that start with "tvault://"
// by shelling out to `tvault get <key> -p <project>`. Non-vault values are
// passed through unchanged. This keeps bearer tokens and other secrets out of
// mcphub.yaml entirely — the config references the vault, the vault holds the
// value.
func resolveVaultHeaders(headers map[string]string) (map[string]string, error) {
	resolved := make(map[string]string, len(headers))
	for name, val := range headers {
		if !strings.HasPrefix(val, vaultScheme) {
			resolved[name] = val
			continue
		}
		project, key, ok := parseVaultRef(val)
		if !ok {
			return nil, fmt.Errorf("header %q: invalid tvault reference %q (expected tvault://<project>/<key> or tvault://<key>)", name, val)
		}
		secret, err := tvaultGet(key, project)
		if err != nil {
			return nil, fmt.Errorf("header %q: resolve %s: %w", name, val, err)
		}
		resolved[name] = secret
	}
	return resolved, nil
}

// parseVaultRef parses a "tvault://<project>/<key>" or "tvault://<key>"
// reference. Returns ok=false if the reference is malformed (empty key).
func parseVaultRef(ref string) (project, key string, ok bool) {
	body := strings.TrimPrefix(ref, vaultScheme)
	if body == "" {
		return "", "", false
	}
	idx := strings.IndexByte(body, '/')
	if idx < 0 {
		// tvault://<key> — active project
		return "", body, true
	}
	project = body[:idx]
	key = body[idx+1:]
	if key == "" {
		return "", "", false
	}
	// "current" is a keyword meaning the active project.
	if project == "current" {
		project = ""
	}
	return project, key, true
}

// tvaultGet shells out to `tvault get <key> [-p <project>]` and returns the
// decrypted value (stdout, trimmed of trailing whitespace). The tvault binary
// must be on PATH and the vault must be unlocked (via TVAULT_PASSPHRASE,
// TVAULT_IDENTITY_KEY, or a running `tvault agent`).
func tvaultGet(key, project string) (string, error) {
	args := []string{"get", key}
	if project != "" {
		args = append(args, "-p", project)
	}
	cmd := exec.Command("tvault", args...)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		if stderr != "" {
			return "", fmt.Errorf("tvault get %q: %s", key, stderr)
		}
		return "", fmt.Errorf("tvault get %q: %w", key, err)
	}
	return strings.TrimSpace(string(out)), nil
}
