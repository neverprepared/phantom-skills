package main

import "github.com/spf13/cobra"

// proposalCmd is the operator lever over the create/prune/promote queue — the
// human gate. Confirm-to-act is the default, so nothing applies to the shared
// registry until an operator (or an autonomous opt-in) approves it here.
//
// Stubs until M4 (telemetry + proposals).
func proposalCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "proposal",
		Short: "Review the create/prune/promote queue (the human gate)",
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List proposals",
		RunE:  stub("M4"),
	}
	list.Flags().String("status", "", "filter: pending|approved|rejected")
	list.Flags().String("kind", "", "filter: create|prune|promote")

	approve := &cobra.Command{
		Use:   "approve <id>",
		Short: "Approve and apply a proposal",
		Args:  cobra.ExactArgs(1),
		RunE:  stub("M4"),
	}

	reject := &cobra.Command{
		Use:   "reject <id>",
		Short: "Reject a proposal",
		Args:  cobra.ExactArgs(1),
		RunE:  stub("M4"),
	}
	reject.Flags().String("reason", "", "reason for rejection")

	c.AddCommand(list, approve, reject)
	return c
}
