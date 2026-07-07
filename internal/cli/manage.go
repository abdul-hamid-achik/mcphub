package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
)

// --- add ------------------------------------------------------------------

func newAddCmd() *cobra.Command {
	var (
		url, transport, description, vault string
		env, tags, vaultOnly               []string
		enabled, disabled, force           bool
	)
	cmd := &cobra.Command{
		Use:   "add <name> [command] [args...]",
		Short: "Register a server in mcphub.yaml",
		Long: `add registers an MCP server.

  mcphub add codemap codemap serve            # stdio server
  mcphub add ctx7 --url https://mcp.ctx7.io   # remote (http) server
  mcphub add db pg-mcp --env DSN=postgres://… --tag data
  mcphub add gh gh-mcp --vault github         # secrets injected via tvault`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, path, err := loadConfig()
			if err != nil {
				return err
			}
			name := args[0]
			if _, exists := c.Servers[name]; exists && !force {
				return fmt.Errorf("server %q already exists (use --force to overwrite)", name)
			}
			if enabled && disabled {
				return fmt.Errorf("--enabled and --disabled are mutually exclusive")
			}
			s := config.Server{
				Enabled:     !disabled,
				Description: description,
				Tags:        tags,
				Vault:       vault,
				VaultOnly:   vaultOnly,
			}
			if url != "" {
				s.URL = url
				s.Transport = transport
			} else {
				if len(args) < 2 {
					return fmt.Errorf("need a command (or --url): mcphub add %s <command> [args...]", name)
				}
				s.Command = args[1]
				s.Args = args[2:]
			}
			envMap, err := parseEnv(env)
			if err != nil {
				return err
			}
			s.Env = envMap
			if c.Servers == nil {
				c.Servers = map[string]config.Server{}
			}
			c.Servers[name] = s
			if err := c.Validate(); err != nil {
				return err
			}
			if err := config.Save(path, c); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added %q. Run `mcphub sync` to apply.\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "remote server URL (instead of a command)")
	cmd.Flags().StringVar(&transport, "transport", "", "remote transport: http or sse (default http)")
	cmd.Flags().StringVar(&description, "description", "", "human description")
	cmd.Flags().StringArrayVar(&env, "env", nil, "environment variable KEY=VALUE (repeatable)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "tag (repeatable)")
	cmd.Flags().StringVar(&vault, "vault", "", "tvault project to inject secrets from at spawn")
	cmd.Flags().StringArrayVar(&vaultOnly, "vault-only", nil, "inject only these secret keys (repeatable)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "add but leave disabled")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing server")
	cmd.Flags().BoolVar(&enabled, "enabled", false, "add the server enabled (default; accepted for compatibility with ecosystem docs)")
	return cmd
}

func parseEnv(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid --env %q (want KEY=VALUE)", p)
		}
		out[k] = v
	}
	return out, nil
}

// --- remove ---------------------------------------------------------------

func newRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Offload a server from mcphub.yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, path, err := loadConfig()
			if err != nil {
				return err
			}
			name := args[0]
			if _, ok := c.Servers[name]; !ok {
				return fmt.Errorf("no such server %q", name)
			}
			delete(c.Servers, name)
			// prune the server from any group memberships
			for g, members := range c.Groups {
				kept := members[:0]
				for _, m := range members {
					if m != name {
						kept = append(kept, m)
					}
				}
				c.Groups[g] = kept
			}
			if err := config.Save(path, c); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %q. Run `mcphub sync` to apply.\n", name)
			return nil
		},
	}
}

// --- groups / use ---------------------------------------------------------

func newGroupsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "groups",
		Short: "List server groups",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := loadConfig()
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(cmd, c.Groups)
			}
			if len(c.Groups) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No groups defined. Add a `groups:` block to mcphub.yaml.")
				return nil
			}
			names := make([]string, 0, len(c.Groups))
			for g := range c.Groups {
				names = append(names, g)
			}
			sort.Strings(names)
			for _, g := range names {
				members := c.Groups[g]
				on := 0
				for _, m := range members {
					if c.Servers[m].Enabled {
						on++
					}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s (%d/%d on): %s\n", g, on, len(members), strings.Join(members, ", "))
			}
			return nil
		},
	}
}

func newUseCmd() *cobra.Command {
	var only bool
	cmd := &cobra.Command{
		Use:   "use <group>",
		Short: "Enable every server in a group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, path, err := loadConfig()
			if err != nil {
				return err
			}
			group := args[0]
			members, ok := c.Groups[group]
			if !ok {
				return fmt.Errorf("no such group %q (see `mcphub groups`)", group)
			}
			inGroup := map[string]bool{}
			for _, m := range members {
				inGroup[m] = true
			}
			for name, s := range c.Servers {
				switch {
				case inGroup[name]:
					s.Enabled = true
				case only:
					s.Enabled = false
				default:
					continue
				}
				c.Servers[name] = s
			}
			if err := config.Save(path, c); err != nil {
				return err
			}
			scope := "enabled group"
			if only {
				scope = "enabled only group"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %q (%s). Run `mcphub sync` to apply.\n", scope, group, strings.Join(members, ", "))
			return nil
		},
	}
	cmd.Flags().BoolVar(&only, "only", false, "also disable every server not in the group")
	return cmd
}
