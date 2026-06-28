package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/mcphub/internal/harness"
	"github.com/abdul-hamid-achik/mcphub/internal/syncer"
)

func newSyncCmd() *cobra.Command {
	var write bool
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
	return cmd
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
}
