package main

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	pkserver "github.com/neverprepared/phantom-skills/internal/server"
)

// serveCmd runs the control-server HTTP daemon: the chi API under /api/skills,
// the shared skill registry (Postgres SoR), and the background worker loop
// that drives the intelligence pipeline. Reads config from PSKILLS_CONFIG_DIR
// (default ~/.config/phantom-skills-server); state under PSKILLS_DATA_DIR.
func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run as the HTTP daemon (control server)",
		Long: `Starts the phantom-skills HTTP daemon: the skills registry API plus the
background workers (detect/author/verify/prune/promote).

Required config dir layout (default ~/.config/phantom-skills-server):

  server.toml
  profiles/<scope>/config.toml   (optional overlay)
  profiles/<scope>/auth.toml     (bearer_token)

State lives under PSKILLS_DATA_DIR (default /var/lib/phantom-skills). The daemon
acquires an exclusive flock under {data}/_daemon/locks so a second instance
can't corrupt shared state.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
			d, err := pkserver.Start(pkserver.StartOpts{
				ConfigDir: pkserver.DefaultConfigDir(),
				DataDir:   pkserver.DefaultDataDir(),
				Logger:    logger,
			})
			if err != nil {
				return err
			}
			return d.Run()
		},
	}
}
