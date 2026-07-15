package main

import "github.com/spf13/cobra"

// syncCmd pulls the current shared skill set down from the daemon and
// materializes it as ~/.claude/skills/<name>/SKILL.md files, which Claude Code
// file-watches and loads natively. Only touches marker-bearing dirs it owns —
// hand-authored local skills are never overwritten or pruned.
//
// Stub until M3 (sync).
func syncCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "sync",
		Short: "Pull the shared skill set into ~/.claude/skills",
		RunE:  stub("M3"),
	}
	c.Flags().Bool("dry-run", false, "show what would change without writing")
	c.Flags().String("skills-dir", "", "target skills dir (default ~/.claude/skills)")
	c.Flags().Bool("prune", false, "remove managed skills no longer in the registry")
	c.Flags().Bool("include-local", false, "also materialize status=local skills")
	return c
}
