package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
	"github.com/abdul-hamid-achik/mcphub/internal/harness"
)

func newOffloadCmd() *cobra.Command {
	var write bool
	cmd := &cobra.Command{
		Use:   "offload [agent...]",
		Short: "Remove gateway-proxied servers from agents, leaving just the mcphub gateway",
		Long: `offload is the second half of "register and offload": it removes the direct
copies of the servers mcphub now proxies from each gateway-mode agent, so the
agent relies purely on the single mcphub gateway. This is where the token
savings land — each agent stops carrying every server's full tool list.

It only removes servers mcphub both proxies AND previously managed in the agent
(tracked in the intelligence store), so a user's hand-added entry that happens
to share a name with a proxied server is never clobbered. Anything mcphub does
NOT proxy (disabled or agent-internal servers like node_repl) is left untouched,
and the mcphub gateway itself is never removed. Dry-run by default; --write
applies after saving a timestamped .bak and updates the managed-entries store.

Run 'mcphub sync --write' first so each agent has the mcphub gateway; offload
skips any agent that doesn't.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := loadConfig()
			if err != nil {
				return err
			}
			proxied := c.EnabledServers()
			if len(proxied) == 0 {
				return fmt.Errorf("no enabled servers to offload")
			}
			// Never include the reserved gateway name in the removal set, even if
			// a downstream server somehow shares it — that would delete the
			// gateway and strand the agent.
			kept := proxied[:0]
			for _, s := range proxied {
				if s != "mcphub" {
					kept = append(kept, s)
				}
			}
			proxiedSet := toSet(kept)

			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()

			targets := args
			if len(targets) == 0 {
				targets = c.AgentNames()
			}
			out := cmd.OutOrStdout()
			anyChange, anyErr := false, false
			for _, name := range targets {
				agent, ok := c.Agents[name]
				if !ok {
					fmt.Fprintf(out, "» %s (error: no such agent)\n", name)
					anyErr = true
					continue
				}
				if agent.Disabled {
					fmt.Fprintf(out, "» %s (disabled, skipped)\n", name)
					continue
				}
				adapter, err := harness.For(agent.Type)
				if err != nil {
					fmt.Fprintf(out, "» %s (error: %v)\n", name, err)
					anyErr = true
					continue
				}
				path := config.ExpandPath(agent.Path)
				if agent.ResolvedMode() != config.ModeGateway {
					fmt.Fprintf(out, "» %s (direct mode — nothing to offload)\n", name)
					continue
				}
				// Safety: never strand an agent — only offload once the gateway is present.
				existing, err := adapter.List(path)
				if err != nil {
					fmt.Fprintf(out, "» %s (error: %v)\n", name, err)
					anyErr = true
					continue
				}
				if !hasServer(existing, "mcphub") {
					fmt.Fprintf(out, "» %s (no mcphub gateway found — run `mcphub sync --write` first; skipped)\n", name)
					continue
				}
				// Only remove servers mcphub BOTH proxies AND previously managed
				// in this agent — so a user's hand-added entry sharing a proxied
				// name is never clobbered.
				managed, err := st.ManagedFor(context.Background(), name)
				if err != nil {
					fmt.Fprintf(out, "» %s (error: %v)\n", name, err)
					anyErr = true
					continue
				}
				owned := intersect(kept, managed)
				// desired=nil leaves the gateway and any non-proxied servers
				// untouched; owned=removable removes only the proxied+managed
				// servers that are actually present.
				plan, err := adapter.Apply(path, nil, owned, !write)
				if err != nil {
					fmt.Fprintf(out, "» %s (error: %v)\n", name, err)
					anyErr = true
					continue
				}
				printOffload(out, name, agent, plan)
				if plan.HasChanges() {
					anyChange = true
				}
				// Keep the store's managed-entries in sync: drop the offloaded
				// servers so the next `sync` sees a consistent owned set.
				if write && plan.Applied {
					remaining := minus(managed, proxiedSet)
					if err := st.SetManaged(context.Background(), name, remaining); err != nil {
						fmt.Fprintf(out, "» %s (warning: store update failed: %v)\n", name, err)
					}
				}
			}
			if !write && anyChange {
				fmt.Fprintln(out, "\nDry run. Re-run with --write to apply (a .bak is saved first).")
			}
			if anyErr {
				return fmt.Errorf("offload completed with errors for some agents")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&write, "write", false, "actually edit the agent config files")
	return cmd
}

func hasServer(servers []harness.MCPServer, name string) bool {
	for _, s := range servers {
		if s.Name == name {
			return true
		}
	}
	return false
}

func printOffload(out io.Writer, name string, agent config.Agent, plan harness.Plan) {
	fmt.Fprintf(out, "» %s  (%s) → %s\n", name, agent.Type, plan.Path)
	if !plan.HasChanges() {
		fmt.Fprintln(out, "    nothing to offload")
		return
	}
	for _, ch := range plan.Changes {
		if ch.Action == harness.ActionRemove {
			fmt.Fprintf(out, "    offload  %s\n", ch.Server)
		}
	}
	if plan.Applied {
		if plan.Backup != "" {
			fmt.Fprintf(out, "    applied (backup: %s)\n", plan.Backup)
		} else {
			fmt.Fprintln(out, "    applied")
		}
	}
}

// toSet converts a string slice to a set for fast lookup.
func toSet(s []string) map[string]bool {
	out := make(map[string]bool, len(s))
	for _, v := range s {
		out[v] = true
	}
	return out
}

// intersect returns the elements of a that are also in b (preserving a's order).
func intersect(a, b []string) []string {
	bs := toSet(b)
	var out []string
	for _, v := range a {
		if bs[v] {
			out = append(out, v)
		}
	}
	return out
}

// minus returns the elements of a that are NOT in set.
func minus(a []string, set map[string]bool) []string {
	var out []string
	for _, v := range a {
		if !set[v] {
			out = append(out, v)
		}
	}
	return out
}
