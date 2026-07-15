// pskillctl is the single binary for phantom-skills: the agent-side stdio MCP
// server, the control-server HTTP daemon, and the operator CLI in one. The
// subcommand picks the mode.
//
// M0 wires the cobra skeleton and a real `version` subcommand. Every other
// verb is a stub that returns a "not implemented (milestone Mx)" error until
// its milestone lands — mirroring the phased build in the plan.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/neverprepared/phantom-skills/internal/version"
)

func main() {
	root := &cobra.Command{
		Use:   "pskillctl",
		Short: "phantom-skills — self-improving skills daemon, MCP server, and operator CLI",
		Long: `pskillctl is a single binary with two top-level groups:

  pskillctl client <cmd>   agent-side commands (MCP server, skill sync, local inspection)
  pskillctl server <cmd>   daemon-side commands (HTTP serve, registry, proposals, db)

The client half runs on every workstation/agent host and talks to the daemon
over HTTP; it never spins up the daemon's HTTP surface or storage backends.
The server half lives on the control server and owns the shared skill registry.

See https://github.com/neverprepared/phantom-skills for the design.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(clientCmd())
	root.AddCommand(serverCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "pskillctl: %v\n", err)
		os.Exit(1)
	}
}

// clientCmd groups every command that runs on a workstation / agent host.
// These talk to a daemon over HTTP (or operate purely on the local
// ~/.claude/skills dir); none of them spin up the daemon.
func clientCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "client",
		Short: "Agent-side commands (MCP server, skill sync, local inspection)",
	}
	c.AddCommand(mcpCmd())
	c.AddCommand(syncCmd())
	c.AddCommand(skillCmd())
	c.AddCommand(clientQueueCmd())
	c.AddCommand(versionCmd())
	return c
}

// serverCmd groups every command that lives on the daemon host: the daemon
// itself plus the operator levers over its state (registry, proposals queue,
// db migrations). These need PSKILLS_CONFIG_DIR / PSKILLS_DATA_DIR access and
// won't work on a workstation that doesn't host the daemon's data dir.
func serverCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "server",
		Short: "Daemon-side commands (HTTP serve, registry, proposals, db)",
	}
	c.AddCommand(serveCmd())
	c.AddCommand(configCmd())
	c.AddCommand(registryCmd())
	c.AddCommand(proposalCmd())
	c.AddCommand(dbCmd())
	c.AddCommand(versionCmd())
	return c
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build metadata",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(),
				"pskillctl %s\n  commit: %s\n  built:  %s\n",
				version.Version, version.Commit, version.BuildDate,
			)
			return nil
		},
	}
}

// stub returns a RunE that reports the verb isn't implemented yet, naming the
// milestone that will land it. Keeps the --help tree honest during the phased
// build without silently no-op'ing.
func stub(milestone string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		return fmt.Errorf("%q is not implemented yet (arrives in milestone %s)", cmd.CommandPath(), milestone)
	}
}

// expandHome turns a leading ~/ into $HOME. Needed because Claude Code's MCP
// env block doesn't expand the shell tilde. Mirrors pbrainctl's helper.
func expandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~/"))
}
