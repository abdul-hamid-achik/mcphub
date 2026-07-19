package hub

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
)

// vaultScheme is the prefix that marks a header value as a tvault reference.
// Syntax: tvault://<project>/<key> or tvault://<key> (active project).
const vaultScheme = "tvault://"

// maxTVaultSecretBytes bounds decrypted header values kept in memory. Header
// credentials are normally tiny; treating larger output as an error prevents a
// broken or hostile helper from making the gateway retain unbounded stdout.
const maxTVaultSecretBytes = 64 * 1024

// resolveVaultHeaders resolves any header values that start with "tvault://"
// by shelling out to `tvault get <key> -p <project>`. Non-vault values are
// passed through unchanged. This keeps bearer tokens and other secrets out of
// mcphub.yaml entirely — the config references the vault, the vault holds the
// value.
func resolveVaultHeaders(ctx context.Context, headers map[string]string) (map[string]string, error) {
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
		secret, err := tvaultGet(ctx, key, project)
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
func tvaultGet(ctx context.Context, key, project string) (string, error) {
	args := []string{"get", key}
	if project != "" {
		args = append(args, "-p", project)
	}
	cmd := exec.CommandContext(ctx, "tvault", args...)
	stdout := &boundedDiagnosticBuffer{limit: maxTVaultSecretBytes}
	stderr := &boundedDiagnosticBuffer{limit: maxDownstreamStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err != nil && ctx.Err() != nil {
		return "", fmt.Errorf("tvault get %q: %w", key, ctx.Err())
	}
	if stdout.Truncated() {
		return "", fmt.Errorf("tvault get %q: secret output exceeded %d-byte limit", key, maxTVaultSecretBytes)
	}
	if err != nil {
		safeNames := startupEnvironmentAllowlist(config.Server{Command: "tvault"})
		if detail := sanitizeStartupDetail(stderr.String(), nil, safeNames, stderr.Truncated()); detail != "" {
			return "", fmt.Errorf("tvault get %q: %s", key, detail)
		}
		return "", fmt.Errorf("tvault get %q failed", key)
	}
	return strings.TrimSpace(stdout.String()), nil
}
