package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/neverprepared/phantom-skills/internal/pgstore"
)

// proposalCmd is the operator lever over the create/prune/promote queue — the
// human gate. Confirm-to-act is the default: nothing applies to the registry
// until an operator approves it here. Operates on the store directly via the
// configured DSN (runs on the daemon host).
func proposalCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "proposal",
		Short: "Review the create/prune/promote queue (the human gate)",
	}
	c.AddCommand(proposalListCmd(), proposalApproveCmd(), proposalRejectCmd())
	return c
}

func openDaemonStore() (*pgstore.Store, error) {
	dsn, err := configuredDSN()
	if err != nil {
		return nil, err
	}
	return pgstore.Open(context.Background(), dsn)
}

func proposalListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "List proposals",
		RunE: func(cmd *cobra.Command, _ []string) error {
			profile, _ := cmd.Flags().GetString("profile")
			status, _ := cmd.Flags().GetString("status")
			kind, _ := cmd.Flags().GetString("kind")
			st, err := openDaemonStore()
			if err != nil {
				return err
			}
			defer st.Close()
			props, err := st.ListProposals(context.Background(), profile, status, kind)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(props) == 0 {
				fmt.Fprintln(out, "(no proposals)")
				return nil
			}
			for _, p := range props {
				fmt.Fprintf(out, "#%-4d %-8s %-8s %-28s %s\n", p.ID, p.Kind, p.Status, p.SkillName, p.Rationale)
			}
			return nil
		},
	}
	c.Flags().String("profile", "personal", "scope profile")
	c.Flags().String("status", "", "filter: pending|approved|rejected")
	c.Flags().String("kind", "", "filter: create|prune|promote")
	return c
}

func proposalApproveCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "approve <id>",
		Short: "Approve and apply a proposal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile, _ := cmd.Flags().GetString("profile")
			by, _ := cmd.Flags().GetString("by")
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid id: %s", args[0])
			}
			st, err := openDaemonStore()
			if err != nil {
				return err
			}
			defer st.Close()
			res, err := st.ApproveProposal(context.Background(), profile, id, by)
			if err != nil {
				return err
			}
			ver := ""
			if res.Version > 0 {
				ver = fmt.Sprintf(" (v%d)", res.Version)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "approved #%d: %s %s -> %s%s\n",
				id, res.Kind, res.SkillName, res.NewStatus, ver)
			return nil
		},
	}
	c.Flags().String("profile", "personal", "scope profile")
	c.Flags().String("by", "operator", "who approved")
	return c
}

func proposalRejectCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "reject <id>",
		Short: "Reject a proposal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile, _ := cmd.Flags().GetString("profile")
			by, _ := cmd.Flags().GetString("by")
			reason, _ := cmd.Flags().GetString("reason")
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid id: %s", args[0])
			}
			st, err := openDaemonStore()
			if err != nil {
				return err
			}
			defer st.Close()
			if err := st.RejectProposal(context.Background(), profile, id, by, reason); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "rejected #%d\n", id)
			return nil
		},
	}
	c.Flags().String("profile", "personal", "scope profile")
	c.Flags().String("by", "operator", "who rejected")
	c.Flags().String("reason", "", "reason for rejection")
	return c
}
