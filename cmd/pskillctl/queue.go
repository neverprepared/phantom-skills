package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/neverprepared/phantom-skills/internal/client"
	"github.com/neverprepared/phantom-skills/internal/client/wqueue"
	"github.com/neverprepared/phantom-skills/internal/config"
)

// clientQueueCmd operates on the agent-side SQLite write-ahead queue that
// buffers usage/proposal writes while the daemon is unreachable. Resolves the
// queue path from the CL_SKILLS_* agent contract.
func clientQueueCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "queue",
		Short: "Inspect and drain the offline write-ahead queue",
	}
	c.AddCommand(queueListCmd(), queueDrainCmd(), queuePurgeCmd())
	return c
}

// openQueue resolves the agent contract and opens the queue.
func openQueue() (*wqueue.Queue, *config.Agent, error) {
	agent, err := config.LoadAgent()
	if err != nil {
		return nil, nil, err
	}
	q, err := wqueue.Open(filepath.Join(agent.StateDir(), "queue"))
	if err != nil {
		return nil, nil, err
	}
	return q, agent, nil
}

func queueListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "List queued writes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			all, _ := cmd.Flags().GetBool("all")
			q, _, err := openQueue()
			if err != nil {
				return err
			}
			defer q.Close()
			items, err := q.List(context.Background(), all, 200)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(queue empty)")
				return nil
			}
			for _, it := range items {
				dead := ""
				if it.Dead {
					dead = " DEAD"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "#%d %s attempts=%d%s %s\n",
					it.ID, it.Kind, it.Attempts, dead, it.LastError)
			}
			return nil
		},
	}
	c.Flags().Bool("all", false, "include dead-lettered items")
	return c
}

func queueDrainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "drain",
		Short: "Force a drain attempt now",
		RunE: func(cmd *cobra.Command, _ []string) error {
			q, agent, err := openQueue()
			if err != nil {
				return err
			}
			defer q.Close()
			cl, err := client.New(client.Opts{BaseURL: agent.API, Token: agent.Token})
			if err != nil {
				return err
			}
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
			drainer := client.NewDrainer(q, cl, &client.Connectivity{}, time.Minute, logger)
			n, err := drainer.DrainOnce(context.Background())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "drained %d item(s)\n", n)
			return nil
		},
	}
}

func queuePurgeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "purge",
		Short: "Remove queued writes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dead, _ := cmd.Flags().GetBool("dead")
			all, _ := cmd.Flags().GetBool("all")
			if !dead && !all {
				return fmt.Errorf("specify --dead (only dead-lettered) or --all (everything)")
			}
			q, _, err := openQueue()
			if err != nil {
				return err
			}
			defer q.Close()
			n, err := q.Purge(context.Background(), dead, all)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "purged %d item(s)\n", n)
			return nil
		},
	}
	c.Flags().Bool("dead", false, "only purge dead-lettered writes")
	c.Flags().Bool("all", false, "purge every queued write")
	return c
}
