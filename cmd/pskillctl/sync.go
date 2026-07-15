package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/neverprepared/phantom-skills/internal/client"
	"github.com/neverprepared/phantom-skills/internal/config"
	"github.com/neverprepared/phantom-skills/internal/syncer"
)

// syncCmd pulls the current shared skill set down from the daemon and
// materializes it as ~/.claude/skills/<slug>/SKILL.md files. Only touches
// marker-bearing dirs it owns — hand-authored local skills are never
// overwritten or pruned.
func syncCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "sync",
		Short: "Pull the shared skill set into ~/.claude/skills",
		RunE: func(cmd *cobra.Command, _ []string) error {
			agent, err := config.LoadAgent()
			if err != nil {
				return err
			}
			skillsDir, _ := cmd.Flags().GetString("skills-dir")
			if skillsDir == "" {
				skillsDir = agent.SkillsDir
			}
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			reset, _ := cmd.Flags().GetBool("reset")

			cl, err := client.New(client.Opts{BaseURL: agent.API, Token: agent.Token})
			if err != nil {
				return err
			}
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
			sy := syncer.New(cl, skillsDir, agent.StateDir(), logger)
			res, err := sy.Sync(context.Background(), syncer.Options{DryRun: dryRun, Reset: reset})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if dryRun {
				for _, ch := range res.Changes {
					fmt.Fprintln(out, "  "+ch)
				}
			}
			fmt.Fprintf(out, "written=%d up-to-date=%d deleted=%d skipped-unmanaged=%d\n",
				res.Written, res.UpToDate, res.Deleted, res.SkippedUnmanaged)
			return nil
		},
	}
	c.Flags().Bool("dry-run", false, "show what would change without writing")
	c.Flags().String("skills-dir", "", "target skills dir (default ~/.claude/skills or CL_SKILLS_DIR)")
	c.Flags().Bool("reset", false, "ignore the saved cursor and pull the full set")
	return c
}
