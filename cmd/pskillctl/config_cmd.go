package main

import (
	"fmt"

	"github.com/spf13/cobra"

	pkserver "github.com/neverprepared/phantom-skills/internal/server"
)

// configCmd groups daemon config operations.
func configCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Daemon configuration operations",
	}
	c.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Parse server.toml + scopes and report problems",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := pkserver.DefaultConfigDir()
			cfg, err := pkserver.LoadServerConfig(dir)
			if err != nil {
				return err
			}
			reg := pkserver.NewRegistry()
			n, err := reg.Load(dir, cfg.Defaults)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "config dir: %s\n", dir)
			fmt.Fprintf(out, "listen:     %s:%d\n", cfg.Server.Host, cfg.Server.Port)
			fmt.Fprintf(out, "postgres:   %s\n", backendLabel(cfg.Postgres.DSN != ""))
			fmt.Fprintf(out, "brain:      %s\n", backendLabel(cfg.Brain.Enabled()))
			fmt.Fprintf(out, "scopes:     %d\n", n)
			for _, b := range reg.Scopes() {
				desc := b.Auth.Description
				if desc != "" {
					desc = "  (" + desc + ")"
				}
				fmt.Fprintf(out, "  - %s%s\n", b.Key, desc)
			}
			fmt.Fprintln(out, "OK")
			return nil
		},
	})
	return c
}

func backendLabel(configured bool) string {
	if configured {
		return "configured"
	}
	return "disabled"
}
