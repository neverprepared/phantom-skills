package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/neverprepared/phantom-skills/internal/client"
	"github.com/neverprepared/phantom-skills/internal/config"
	"github.com/neverprepared/phantom-skills/internal/skillfile"
)

// skillCmd is the client-side parent for inspecting locally-synced skills.
func skillCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "skill",
		Short: "Inspect locally-synced skills (list, show, diff)",
	}
	c.AddCommand(skillListLocalCmd(), skillShowCmd(), skillDiffCmd())
	return c
}

// agentSkillsDir resolves the local skills dir from the agent contract.
func agentSkillsDir() (string, *config.Agent, error) {
	agent, err := config.LoadAgent()
	if err != nil {
		return "", nil, err
	}
	return agent.SkillsDir, agent, nil
}

func skillListLocalCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List skills currently present in the local skills dir",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, _, err := agentSkillsDir()
			if err != nil {
				return err
			}
			entries, err := os.ReadDir(dir)
			if os.IsNotExist(err) {
				fmt.Fprintln(cmd.OutOrStdout(), "(no skills dir yet)")
				return nil
			}
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			found := 0
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				data, err := os.ReadFile(filepath.Join(dir, e.Name(), "SKILL.md"))
				if err != nil {
					continue
				}
				fm, _, perr := skillfile.Parse(data)
				if perr != nil {
					continue
				}
				found++
				name, _ := fm["name"].(string)
				if name == "" {
					name = e.Name()
				}
				marker, managed := skillfile.MarkerOf(fm)
				tag := "local"
				if managed {
					tag = marker.Status + " (managed)"
				}
				fmt.Fprintf(out, "%-28s %s\n", name, tag)
			}
			if found == 0 {
				fmt.Fprintln(out, "(no skills)")
			}
			return nil
		},
	}
}

func skillShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Print a synced SKILL.md",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, _, err := agentSkillsDir()
			if err != nil {
				return err
			}
			path := filepath.Join(dir, skillfile.Slugify(args[0]), "SKILL.md")
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("not found locally: %s", args[0])
			}
			_, _ = cmd.OutOrStdout().Write(data)
			return nil
		},
	}
}

func skillDiffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff <name>",
		Short: "Compare the local skill against the registry's current version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			dir, agent, err := agentSkillsDir()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			path := filepath.Join(dir, skillfile.Slugify(name), "SKILL.md")
			localData, localErr := os.ReadFile(path)

			cl, err := client.New(client.Opts{BaseURL: agent.API, Token: agent.Token})
			if err != nil {
				return err
			}
			resp, err := cl.GetSkill(context.Background(), name)
			if err != nil {
				return err
			}

			switch {
			case os.IsNotExist(localErr):
				fmt.Fprintf(out, "%s: not present locally; registry has v%d\n", name, resp.Skill.Version)
			case localErr != nil:
				return localErr
			default:
				fm, _, _ := skillfile.Parse(localData)
				marker, _ := skillfile.MarkerOf(fm)
				if resp.Version != nil && marker.SHA == resp.Version.SHA {
					fmt.Fprintf(out, "%s: up to date (v%d)\n", name, resp.Skill.Version)
				} else {
					fmt.Fprintf(out, "%s: DRIFT — local sha %s, registry v%d sha %s\n",
						name, shortSHA(marker.SHA), resp.Skill.Version, shortSHA(registrySHA(resp)))
				}
			}
			return nil
		},
	}
}

func registrySHA(resp *client.GetSkillResponse) string {
	if resp.Version != nil {
		return resp.Version.SHA
	}
	return ""
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	if s == "" {
		return "(none)"
	}
	return s
}
