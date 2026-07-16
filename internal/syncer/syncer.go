// Package syncer reconciles mcphub.yaml into agent harness configs. It is the
// shared engine behind both `mcphub sync` (CLI) and the Studio TUI's sync
// action, so the two can never drift in behavior.
package syncer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/harness"
	"github.com/abdul-hamid-achik/mcphub/internal/store"
)

// AgentResult is the outcome of reconciling one agent.
type AgentResult struct {
	Agent   string
	Type    string
	Mode    string
	Skipped bool // agent is disabled
	Plan    harness.Plan
	Err     error
}

// generatePlanID returns a short unique plan ID for a sync run.
func generatePlanID(agent string) string {
	return fmt.Sprintf("plan_%d_%s", time.Now().UnixNano(), agent)
}

// Self returns the path sync writes as the gateway command in harness
// configs. os.Executable() alone is hazardous here: run sync from a
// Caskroom-versioned path (or any duplicate install) and every harness would
// churn to that unstable location on the next --write. When the executable's
// basename resolves on PATH to the same underlying file, the PATH location
// wins — it is the stable, upgrade-surviving name (/opt/homebrew/bin/mcphub
// is a symlink homebrew repoints on every upgrade). A genuinely different
// binary (a dev build outside PATH) keeps its explicit path: pointing
// harnesses at it is then an intentional act, not an accident of cwd.
func Self() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return normalizeSelf(exe), nil
}

func normalizeSelf(exe string) string {
	fromPath, err := exec.LookPath(filepath.Base(exe))
	if err != nil {
		return exe
	}
	realPath, err := filepath.EvalSymlinks(fromPath)
	if err != nil {
		return exe
	}
	realExe, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe
	}
	if realPath == realExe {
		return fromPath
	}
	return exe
}

// Desired computes the server entries mcphub wants present in an agent: in
// gateway mode a single 'mcphub' server (self), in direct mode every enabled
// downstream server verbatim — filtered to the agent's `servers` allowlist
// when one is set. When the agent has any routing config (servers or tools),
// the gateway entry is launched with `--agent <name>` so the spawned gateway
// can scope its advertisement and lazy meta-tools to that agent.
func Desired(c *config.Config, name string, agent config.Agent, self string) []harness.MCPServer {
	if agent.ResolvedMode() == config.ModeGateway {
		args := []string{"mcp", "serve"}
		if agent.HasRouting() {
			args = append(args, "--agent", name)
		}
		return []harness.MCPServer{{
			Name:    "mcphub",
			Command: self,
			Args:    args,
		}}
	}
	var out []harness.MCPServer
	for _, name := range agent.AllowedServers(c.EnabledServers()) {
		s := c.Servers[name]
		// SpawnCommand applies tvault wrapping when the server uses a vault, so
		// a directly-written agent also launches it with secrets injected.
		command, cargs := s.SpawnCommand()
		out = append(out, harness.MCPServer{
			Name:      name,
			Command:   command,
			Args:      cargs,
			Env:       s.Env,
			URL:       s.URL,
			Transport: s.Transport,
		})
	}
	return out
}

// Names returns the names of a desired server set.
func Names(servers []harness.MCPServer) []string {
	out := make([]string, len(servers))
	for i, s := range servers {
		out[i] = s.Name
	}
	return out
}

// Reconcile plans (and, when write is true, applies) the sync for the given
// agents. An empty agents slice means every agent in the config. A nonexistent
// named agent yields an AgentResult with Err set rather than aborting the run,
// so callers can render a per-agent report. self is the absolute path to the
// mcphub binary (used for gateway entries).
func Reconcile(ctx context.Context, c *config.Config, st *store.Store, self string, agents []string, write bool) []AgentResult {
	targets := agents
	if len(targets) == 0 {
		targets = c.AgentNames()
	}
	results := make([]AgentResult, 0, len(targets))
	for _, name := range targets {
		r := AgentResult{Agent: name}
		agent, ok := c.Agents[name]
		if !ok {
			r.Err = errUnknownAgent(name)
			results = append(results, r)
			continue
		}
		r.Type, r.Mode = agent.Type, string(agent.ResolvedMode())
		if agent.Disabled {
			r.Skipped = true
			results = append(results, r)
			continue
		}
		adapter, err := harness.For(agent.Type)
		if err != nil {
			r.Err = err
			results = append(results, r)
			continue
		}
		desired := Desired(c, name, agent, self)
		owned, err := st.ManagedFor(ctx, name)
		if err != nil {
			r.Err = err
			results = append(results, r)
			continue
		}
		plan, err := adapter.Apply(config.ExpandPath(agent.Path), desired, owned, !write)
		if err == nil {
			plan.PlanID = generatePlanID(name)
		}
		r.Plan, r.Err = plan, err
		if err == nil && write && plan.Applied {
			// Post-write verification + automatic backup restore on bookkeeping
			// failure (SPEC §8.3): if the config was written but the ownership
			// store can't be updated, restore the backup so the config and the
			// bookkeeping stay consistent.
			if err := st.SetManaged(ctx, name, Names(desired)); err != nil {
				r.Err = fmt.Errorf("bookkeeping failed after apply: %w (backup restored)", err)
				if plan.Backup != "" {
					if restoreErr := restoreBackup(plan.Backup, plan.Path); restoreErr != nil {
						r.Err = fmt.Errorf("bookkeeping failed: %w AND backup restore failed: %v", err, restoreErr)
					}
				}
			} else {
				_ = st.LogSync(ctx, name, r.Mode, Names(desired), false)
				if plan.Backup != "" {
					// Ties the plan ID to the exact pre-apply backup so
					// `sync --rollback <planId>` restores that file, not
					// whatever backup happens to be newest.
					_ = st.RecordPlanBackup(ctx, plan.PlanID, name, plan.Path, plan.Backup)
				}
			}
		}
		results = append(results, r)
	}
	return results
}

type unknownAgentError struct{ name string }

func (e unknownAgentError) Error() string { return "no such agent " + e.name }

func errUnknownAgent(name string) error { return unknownAgentError{name} }

// restoreBackup copies a backup file back to the config path, undoing an
// apply that succeeded at the file level but failed at the bookkeeping level
// (SPEC §8.3: automatic backup restore on bookkeeping failure).
func restoreBackup(backupPath, configPath string) error {
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("read backup %s: %w", backupPath, err)
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return fmt.Errorf("restore backup to %s: %w", configPath, err)
	}
	return nil
}
