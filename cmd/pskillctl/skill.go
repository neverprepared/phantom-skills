package main

import "github.com/spf13/cobra"

// skillCmd is the client-side parent for inspecting locally-synced skills.
// Mirrors what `server registry` does for the daemon side.
//
// Stubs until M3 (sync).
func skillCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "skill",
		Short: "Inspect locally-synced skills (list, show, diff)",
	}
	c.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List skills currently synced into the local skills dir",
			RunE:  stub("M3"),
		},
		&cobra.Command{
			Use:   "show <name>",
			Short: "Print a synced SKILL.md and its registry status",
			Args:  cobra.ExactArgs(1),
			RunE:  stub("M3"),
		},
		&cobra.Command{
			Use:   "diff <name>",
			Short: "Show the local vs registry version diff for a skill",
			Args:  cobra.ExactArgs(1),
			RunE:  stub("M3"),
		},
	)
	return c
}
