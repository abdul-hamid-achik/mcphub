package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/harness"
	"github.com/abdul-hamid-achik/mcphub/internal/syncer"
)

func newSyncCmd() *cobra.Command {
	var write bool
	var resume, rollback string
	cmd := &cobra.Command{
		Use:   "sync [agent...]",
		Short: "Write server config into agent harnesses (dry-run by default)",
		Long: `sync reconciles every agent harness with mcphub.yaml.

By default it is a DRY RUN: it prints the diff it would apply and changes
nothing. Pass --write to actually edit the files (a timestamped .bak is written
first). Name one or more agents to limit the scope; with no names, all enabled
agents are synced.

In gateway mode an agent is given a single 'mcphub' server that proxies the
rest. In direct mode every enabled server is written into the agent.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// --resume <planId>: extract the agent + re-sync with --write.
			if resume != "" {
				agent := agentFromPlanID(resume)
				if agent == "" {
					return fmt.Errorf("could not extract agent name from plan ID %q", resume)
				}
				args = []string{agent}
				write = true
			}
			// --rollback <planId>: restore the exact backup recorded for that
			// plan; fall back to the newest backup for the agent (with an
			// explicit note) when the plan predates backup tracking.
			if rollback != "" {
				agent := agentFromPlanID(rollback)
				if agent == "" {
					return fmt.Errorf("could not extract agent name from plan ID %q", rollback)
				}
				c, _, err := loadConfig()
				if err != nil {
					return err
				}
				agentCfg, ok := c.Agents[agent]
				if !ok {
					return fmt.Errorf("agent %q not found in config", agent)
				}
				path := config.ExpandPath(agentCfg.Path)
				out := cmd.OutOrStdout()

				st, err := openStore()
				if err != nil {
					return err
				}
				defer st.Close()
				if _, recordedPath, backup, err := st.PlanBackup(context.Background(), rollback); err == nil {
					if err := restoreBackupFile(backup, recordedPath); err != nil {
						return fmt.Errorf("rollback restore: %w", err)
					}
					fmt.Fprintf(out, "rolled back %s: restored %s from %s\n", agent, recordedPath, backup)
					return nil
				}
				backup, err := latestBackup(path)
				if err != nil {
					return fmt.Errorf("rollback: plan %s has no recorded backup and %w", rollback, err)
				}
				if err := restoreBackupFile(backup, path); err != nil {
					return fmt.Errorf("rollback restore: %w", err)
				}
				fmt.Fprintf(out, "note: plan %s has no recorded backup (dry run, no-op apply, or pre-tracking); restoring the most recent backup instead\n", rollback)
				fmt.Fprintf(out, "rolled back %s: restored %s from %s\n", agent, path, backup)
				return nil
			}
			c, _, err := loadConfig()
			if err != nil {
				return err
			}
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()

			self, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locate mcphub binary: %w", err)
			}

			results := syncer.Reconcile(context.Background(), c, st, self, args, write)
			out := cmd.OutOrStdout()
			anyChange, anyErr := false, false
			for _, r := range results {
				printResult(out, r)
				if r.Err != nil {
					anyErr = true
				}
				if r.Plan.HasChanges() {
					anyChange = true
				}
			}
			if !write && anyChange {
				fmt.Fprintln(out, "\nDry run. Re-run with --write to apply (a .bak is saved first).")
			}
			if anyErr {
				return fmt.Errorf("sync completed with errors")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&write, "write", false, "actually edit the agent config files")
	cmd.Flags().StringVar(&resume, "resume", "", "re-sync the agent named in a plan ID (e.g. plan_1234567890_claude)")
	cmd.Flags().StringVar(&rollback, "rollback", "", "restore the backup for a plan ID's agent")
	return cmd
}

// agentFromPlanID extracts the agent name from a plan ID like
// "plan_1234567890123456789_claude". Returns "" if the format is invalid.
func agentFromPlanID(planID string) string {
	parts := strings.SplitN(planID, "_", 3)
	if len(parts) != 3 || parts[0] != "plan" {
		return ""
	}
	return parts[2]
}

// latestBackup finds the most recent backup for a config path. Backups are
// written by harness.backup as <path>.bak-<timestamp> (with a -N suffix on
// same-second collisions), so match on the ".bak" prefix rather than a
// suffix. Ties on mtime (one-second timestamp resolution) fall back to the
// lexically greatest name, which sorts the -N collision suffixes last.
func latestBackup(configPath string) (string, error) {
	dir := filepath.Dir(configPath)
	base := filepath.Base(configPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read config dir: %w", err)
	}
	var best string
	var bestTime time.Time
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, base+".bak") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		mt := info.ModTime()
		if best == "" || mt.After(bestTime) || (mt.Equal(bestTime) && name > filepath.Base(best)) {
			best = filepath.Join(dir, name)
			bestTime = mt
		}
	}
	if best == "" {
		return "", fmt.Errorf("no backup found for %s", configPath)
	}
	return best, nil
}

// restoreBackupFile copies a backup file back to the config path.
func restoreBackupFile(backupPath, configPath string) error {
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}
	return os.WriteFile(configPath, data, 0o644)
}

func printResult(out io.Writer, r syncer.AgentResult) {
	if r.Err != nil {
		fmt.Fprintf(out, "» %s  error: %v\n", r.Agent, r.Err)
		return
	}
	if r.Skipped {
		fmt.Fprintf(out, "» %s (disabled, skipped)\n", r.Agent)
		return
	}
	fmt.Fprintf(out, "» %s  (%s, %s) → %s\n", r.Agent, r.Type, r.Mode, r.Plan.Path)
	if !r.Plan.HasChanges() {
		fmt.Fprintln(out, "    up to date")
		return
	}
	for _, ch := range r.Plan.Changes {
		if ch.Action == harness.ActionUnchanged {
			continue
		}
		fmt.Fprintf(out, "    %-8s %s\n", ch.Action, ch.Server)
	}
	if r.Plan.Applied {
		if r.Plan.Backup != "" {
			fmt.Fprintf(out, "    applied (backup: %s)\n", r.Plan.Backup)
		} else {
			fmt.Fprintln(out, "    applied")
		}
	}
	if r.Plan.PlanID != "" {
		fmt.Fprintf(out, "    plan: %s\n", r.Plan.PlanID)
	}
}
