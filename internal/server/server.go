// Package server is the phantom-skills control-server daemon: the chi HTTP API
// under /api/skills, the bearer→scope auth registry, and (from M1b) the
// Postgres-backed skills registry plus the background pipeline workers.
//
// M1a wires the daemon lifecycle (flock exclusivity, config + registry load,
// router, SIGHUP reload, graceful drain) and the unauthenticated /health plus
// an authenticated /whoami. Skills CRUD, migrations, telemetry, proposals, and
// the pipeline seam land in later milestones.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gofrs/flock"

	"github.com/neverprepared/phantom-skills/internal/pgstore"
	"github.com/neverprepared/phantom-skills/internal/version"
)

// Daemon is one running control-server instance. Start() acquires the global
// flock, loads config + the scope registry, and builds the router; Run() blocks
// on the signal loop until SIGINT/SIGTERM (SIGHUP reloads the registry).
type Daemon struct {
	ConfigDir string
	DataDir   string
	Logger    *slog.Logger

	cfg      *ServerConfig
	registry *Registry
	store    *pgstore.Store // nil when no [postgres] dsn configured
	router   chi.Router
	srv      *http.Server
	flock    *flock.Flock
}

// StartOpts groups inputs to Start. All fields required.
type StartOpts struct {
	ConfigDir string
	DataDir   string
	Logger    *slog.Logger
}

// Start loads config, acquires the global flock (fail-fast if another daemon
// holds it), loads the scope registry, and constructs the http.Server. It does
// NOT call ListenAndServe — Run() does, so callers can wire the router in tests
// without binding a port.
func Start(opts StartOpts) (*Daemon, error) {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	cfg, err := LoadServerConfig(opts.ConfigDir)
	if err != nil {
		return nil, err
	}

	d := &Daemon{
		ConfigDir: opts.ConfigDir,
		DataDir:   opts.DataDir,
		Logger:    opts.Logger,
		cfg:       cfg,
		registry:  NewRegistry(),
	}

	lockPath := filepath.Join(opts.DataDir, "_daemon", "locks", "skills-server.pid")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("server: create lock dir: %w", err)
	}
	lk := flock.New(lockPath)
	locked, err := lk.TryLock()
	if err != nil {
		return nil, fmt.Errorf("server: acquire global flock: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("server: another phantom-skills daemon holds %s", lockPath)
	}
	d.flock = lk
	// Record our PID so ops scripts can find us. Best-effort.
	_ = os.WriteFile(lockPath, []byte(strconv.Itoa(os.Getpid())), 0o644)

	n, err := d.registry.Load(opts.ConfigDir, cfg.Defaults)
	if err != nil {
		_ = lk.Unlock()
		return nil, err
	}
	d.Logger.Info("phantom-skills: registry loaded", slog.Int("scopes", n))

	// Open the skills registry store if Postgres is wired. Absent DSN leaves
	// d.store nil — /health reports it disabled and skills endpoints 503.
	if dsn := cfg.Postgres.DSN; dsn != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		store, err := pgstore.Open(ctx, dsn)
		cancel()
		if err != nil {
			_ = lk.Unlock()
			return nil, err
		}
		d.store = store
		d.Logger.Info("phantom-skills: postgres store opened")
	}

	d.router = d.buildRouter()
	d.srv = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:           d.router,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return d, nil
}

func (d *Daemon) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Route("/api/skills", func(r chi.Router) {
		// Unauthenticated liveness.
		r.Get("/health", d.handleHealth)

		// Authenticated surface. /sync, /usage, /proposals land in later
		// milestones; skills CRUD + /whoami are live now.
		r.Group(func(r chi.Router) {
			r.Use(AuthMiddleware(d.registry))
			r.Get("/whoami", d.handleWhoami)
			r.Get("/skills", d.handleListSkills)
			r.Post("/skills", d.handleCreateSkill)
			r.Get("/skills/{name}", d.handleGetSkill)
			r.Put("/skills/{name}", d.handleUpdateSkill)
			r.Delete("/skills/{name}", d.handleRetireSkill)
			r.Get("/skills/{name}/versions", d.handleListVersions)
		})
	})
	return r
}

// Run binds the port and blocks on the signal loop until SIGINT/SIGTERM.
// SIGHUP reloads the scope registry without dropping connections.
func (d *Daemon) Run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	hupCh := make(chan os.Signal, 1)
	signal.Notify(hupCh, syscall.SIGHUP)
	defer signal.Stop(hupCh)

	srvErr := make(chan error, 1)
	go func() {
		d.Logger.Info("phantom-skills: listening", slog.String("addr", d.srv.Addr))
		if err := d.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			srvErr <- err
		}
	}()

	for {
		select {
		case <-hupCh:
			if _, err := d.registry.Load(d.ConfigDir, d.cfg.Defaults); err != nil {
				d.Logger.Warn("phantom-skills: SIGHUP reload failed (keeping prior registry)", slog.String("err", err.Error()))
			} else {
				d.Logger.Info("phantom-skills: SIGHUP registry reloaded")
			}
		case err := <-srvErr:
			_ = d.Shutdown(context.Background())
			return err
		case <-ctx.Done():
			return d.Shutdown(context.Background())
		}
	}
}

// Shutdown gracefully drains the HTTP server and releases the global flock. A
// nil ctx is treated as context.Background() so cleanup paths can call
// Shutdown(nil) without panicking.
func (d *Daemon) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var firstErr error
	if d.srv != nil {
		if err := d.srv.Shutdown(shutdownCtx); err != nil {
			firstErr = err
			d.Logger.Warn("phantom-skills: http drain error", slog.String("err", err.Error()))
		}
	}
	if d.store != nil {
		d.store.Close()
	}
	if d.flock != nil {
		if err := d.flock.Unlock(); err != nil {
			d.Logger.Warn("phantom-skills: release global flock", slog.String("err", err.Error()))
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Router exposes the built router for tests (httptest.NewServer(d.Router())).
func (d *Daemon) Router() chi.Router { return d.router }

// handleHealth reports liveness plus which optional backends are configured.
// The Postgres and phantom-brain checks become real pings in later milestones;
// today they report configured/disabled from the parsed config.
func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	pg := "disabled"
	if d.store != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := d.store.Ping(ctx); err != nil {
			pg = "unreachable"
		} else {
			pg = "ok"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"version":  version.Version,
		"postgres": pg,
		"brain":    backendState(d.cfg.Brain.Enabled()),
		"scopes":   len(d.registry.Scopes()),
	})
}

// handleWhoami echoes the resolved scope for the caller's token. Exists so the
// auth path is testable before the CRUD handlers land.
func (d *Daemon) handleWhoami(w http.ResponseWriter, r *http.Request) {
	binding, ok := BindingFromContext(r.Context())
	if !ok {
		WriteErrorEnvelope(w, http.StatusInternalServerError, ErrCodeInternal, "missing scope binding", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"profile":  binding.Key.Profile,
		"skillset": binding.Key.Skillset,
	})
}

func backendState(configured bool) string {
	if configured {
		return "configured"
	}
	return "disabled"
}
