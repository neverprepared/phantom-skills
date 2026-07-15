package main

import "github.com/spf13/cobra"

// clientQueueCmd operates on the agent-side SQLite write-ahead queue that
// buffers usage/proposal writes while the daemon is unreachable.
//
// Stubs until M2 (agent MCP + client + wqueue).
func clientQueueCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "queue",
		Short: "Inspect and drain the offline write-ahead queue",
	}
	c.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List queued writes",
			RunE:  stub("M2"),
		},
		&cobra.Command{
			Use:   "drain",
			Short: "Force a drain attempt now",
			RunE:  stub("M2"),
		},
		func() *cobra.Command {
			p := &cobra.Command{
				Use:   "purge",
				Short: "Remove queued writes",
				RunE:  stub("M2"),
			}
			p.Flags().Bool("dead", false, "only purge dead-lettered writes")
			p.Flags().Bool("all", false, "purge every queued write")
			return p
		}(),
	)
	return c
}
