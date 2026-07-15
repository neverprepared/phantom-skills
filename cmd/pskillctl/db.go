package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/neverprepared/phantom-skills/internal/pgstore"
	pkserver "github.com/neverprepared/phantom-skills/internal/server"
)

// dbCmd groups daemon Postgres schema operations (golang-migrate).
func dbCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "db",
		Short: "Daemon database schema operations",
	}
	c.AddCommand(
		&cobra.Command{
			Use:   "migrate",
			Short: "Apply pending migrations",
			RunE: func(cmd *cobra.Command, _ []string) error {
				dsn, err := configuredDSN()
				if err != nil {
					return err
				}
				if err := pgstore.Migrate(dsn); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "migrations applied")
				return nil
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show migration status",
			RunE: func(cmd *cobra.Command, _ []string) error {
				dsn, err := configuredDSN()
				if err != nil {
					return err
				}
				version, dirty, err := pgstore.MigrationStatus(dsn)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "schema version: %d\ndirty:          %t\n", version, dirty)
				return nil
			},
		},
	)
	return c
}

// configuredDSN resolves the Postgres DSN from server.toml (+ PSKILLS_POSTGRES_DSN
// override applied by LoadServerConfig). Errors if none is configured.
func configuredDSN() (string, error) {
	cfg, err := pkserver.LoadServerConfig(pkserver.DefaultConfigDir())
	if err != nil {
		return "", err
	}
	if cfg.Postgres.DSN == "" {
		return "", fmt.Errorf("no postgres dsn configured (set [postgres].dsn in server.toml or PSKILLS_POSTGRES_DSN)")
	}
	return cfg.Postgres.DSN, nil
}
