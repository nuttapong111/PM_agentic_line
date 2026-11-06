// Command server runs the LINE PM Assistant: HTTP routes plus the once-a-minute scheduler.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	_ "time/tzdata" // the container may not ship zoneinfo

	"github.com/nuttapong111/pm_agentic_line/internal/adapter/anthropic"
	"github.com/nuttapong111/pm_agentic_line/internal/adapter/builtin"
	"github.com/nuttapong111/pm_agentic_line/internal/adapter/crypto"
	"github.com/nuttapong111/pm_agentic_line/internal/adapter/filestore"
	"github.com/nuttapong111/pm_agentic_line/internal/adapter/google"
	"github.com/nuttapong111/pm_agentic_line/internal/adapter/line"
	"github.com/nuttapong111/pm_agentic_line/internal/adapter/notion"
	"github.com/nuttapong111/pm_agentic_line/internal/adapter/plane"
	"github.com/nuttapong111/pm_agentic_line/internal/adapter/postgres"
	"github.com/nuttapong111/pm_agentic_line/internal/config"
	"github.com/nuttapong111/pm_agentic_line/internal/delivery/httpapi"
	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
	"github.com/nuttapong111/pm_agentic_line/internal/usecase"
	"github.com/nuttapong111/pm_agentic_line/web"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	config.LoadDotEnv(".env")
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return errors.New("TZ_NAME is not a known time zone: " + cfg.Timezone)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hc := &http.Client{Timeout: 60 * time.Second}
	secrets := crypto.New(cfg.EncryptionKey)
	defaults := domain.PrefDefaults{Nicknames: cfg.Nicknames, DigestCron: cfg.DigestCron, StaleDays: cfg.StaleDays, BugLabel: cfg.BugLabel}
	file := filestore.New(cfg.DataDir)

	// Persistence: Postgres when DATABASE_URL is set (state carried over from the old file once), else a JSON file.
	var (
		db      *postgres.DB
		state   *postgres.StateStore
		store   *usecase.Store
		records port.RecordRepo  = usecase.NoRecords{}
		history port.HistoryRepo = usecase.NoHistory{}
		bt      *builtin.Provider
		btAdmin port.BuiltinAdmin
		btTasks port.TaskProvider
	)
	if cfg.DatabaseURL != "" {
		if db, err = postgres.Open(ctx, cfg.DatabaseURL); err != nil {
			return errors.New("database: " + err.Error())
		}
		defer db.Close()
		state = db.State()
		if store, err = usecase.NewStore(ctx, state, file, defaults, time.Now); err != nil {
			return err
		}
		records, history = db.Records(), db.History()
		bt = builtin.New(db.Pool, cfg.PublicURL, store.Nicknames)
		btAdmin, btTasks = bt, bt
		if err := bt.Migrate(ctx); err != nil {
			slog.Error("built-in tables failed", "err", err)
		}
	} else {
		slog.Warn("DATABASE_URL not set: state is kept in a file, history and records are off")
		if store, err = usecase.NewStore(ctx, file, nil, defaults, time.Now); err != nil {
			return err
		}
	}

	planeFactory := func(baseURL, ws, token string, follow []string) port.TaskProvider {
		return plane.New(baseURL, token, ws, follow, hc)
	}
	tasks := usecase.NewTasks(cfg, store, secrets, history, planeFactory, btTasks)
	lineClient := line.New(cfg.LineAccessToken, hc)
	app := usecase.New(usecase.Deps{
		Cfg: cfg, Loc: loc, Now: time.Now, Store: store, Tasks: tasks, Builtin: btAdmin,
		Records: records, History: history, Secrets: secrets,
		GoogleAPI: google.New(hc), NotionAPI: notion.New(hc), LLM: anthropic.New(hc), View: line.Presenter{},
	})
	srv := &httpapi.Server{App: app, Cfg: cfg, Line: lineClient, DashboardHTML: web.DashboardHTML, SettingsHTML: web.SettingsHTML}

	// Pick the task tool now so every log row is tagged with its workspace.
	if p := tasks.Current(); p.Kind() == port.KindNone {
		slog.Info("task tool: not connected yet (open Settings)")
	} else {
		go func() {
			me, err := p.Me(ctx)
			if err != nil {
				slog.Error("task tool check failed", "kind", p.Kind(), "err", err)
				return
			}
			slog.Info("task tool OK", "kind", p.Kind(), "account", me.DisplayName)
		}()
	}
	if cfg.RichMenuAuto {
		go func() {
			id, err := lineClient.EnsureRichMenu(ctx, app.DashboardURL(""), web.RichMenuPNG, false)
			if err != nil {
				slog.Error("rich menu setup failed", "err", err)
				return
			}
			slog.Info("rich menu ready", "id", id)
		}()
	}
	go schedule(ctx, func() { app.Minute(ctx, srv.Push) })

	hs := &http.Server{Addr: ":" + strconv.Itoa(cfg.Port), Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sc, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hs.Shutdown(sc)
	}()
	slog.Info("LINE PM Assistant listening", "port", cfg.Port)
	slog.Info("pages", "dashboard", cfg.PublicURL+"/dashboard?t=<DASHBOARD_TOKEN>", "settings", cfg.PublicURL+"/settings?t=<DASHBOARD_TOKEN>")
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if state != nil {
		state.Flush() // the last state write finishes before the pool closes
	}
	return nil
}

// schedule calls fn at the start of every minute until ctx ends.
func schedule(ctx context.Context, fn func()) {
	for {
		now := time.Now()
		next := now.Truncate(time.Minute).Add(time.Minute)
		select {
		case <-ctx.Done():
			return
		case <-time.After(next.Sub(now)):
			fn()
		}
	}
}
