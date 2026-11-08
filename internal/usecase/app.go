// Package usecase holds the application logic: the agent loop, tools, computed views,
// scheduled alerts and settings. It depends on domain and on the interfaces in port only.
package usecase

import (
	"context"
	"net/url"
	"sync"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/config"
	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

// Deps are the collaborators the use cases need.
type Deps struct {
	Cfg       config.Config
	Loc       *time.Location
	Now       func() time.Time
	Store     *Store
	Tasks     *Tasks
	Builtin   port.BuiltinAdmin // nil without a database
	Records   port.RecordRepo
	History   port.HistoryRepo
	Secrets   port.Secrets
	GoogleAPI port.GoogleAPI
	NotionAPI port.NotionAPI
	LLM       port.LLM
	View      port.Presenter
}

// App is the application: every use case is a method on it, grouped by file.
type App struct {
	Deps
	Google *Google
	Notion *Notion

	tools     []port.ToolDef
	toolNames map[string]bool
	writes    map[string]bool
	tickMu    sync.Mutex
	dash      dashCache
	dashMu    sync.Mutex // one background refresh round at a time
}

func New(d Deps) *App {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Records == nil {
		d.Records = NoRecords{}
	}
	if d.History == nil {
		d.History = NoHistory{}
	}
	a := &App{Deps: d}
	a.Google = &Google{api: d.GoogleAPI, store: d.Store, secrets: d.Secrets, cfg: d.Cfg, now: d.Now, loc: d.Loc}
	a.Notion = &Notion{api: d.NotionAPI, store: d.Store, secrets: d.Secrets}
	a.initTools()
	return a
}

// tasks is the provider currently configured.
func (a *App) tasks() port.TaskProvider { return a.Tasks.Current() }

// ws is the workspace key new records are tagged with.
func (a *App) ws() string { return a.Tasks.Workspace() }

func (a *App) today() string { return domain.DateIn(a.Now(), a.Loc) }

func (a *App) nowMs() int64 { return a.Now().UnixMilli() }

// DashboardURL links the dashboard page, optionally on a project.
func (a *App) DashboardURL(projectID string) string {
	q := url.Values{"t": {a.Cfg.DashboardToken}}
	if projectID != "" {
		q.Set("project", projectID)
	}
	return a.Cfg.PublicURL + "/dashboard?" + q.Encode()
}

// SettingsURL links the settings page, optionally on a section.
func (a *App) SettingsURL(section string) string {
	u := a.Cfg.PublicURL + "/settings?t=" + url.QueryEscape(a.Cfg.DashboardToken)
	if section != "" {
		u += "#" + section
	}
	return u
}

// PreviewURL links the full view of a draft.
func (a *App) PreviewURL(id string) string {
	return a.Cfg.PublicURL + "/preview/" + id + "?t=" + url.QueryEscape(a.Cfg.DashboardToken)
}

func (a *App) logActivity(ctx context.Context, kind, summary string, projectID *string, detail any) {
	a.History.LogActivity(ctx, a.ws(), kind, summary, projectID, detail)
}

// findProject returns the followed project with this id.
func (a *App) findProject(ctx context.Context, id string) (*domain.Project, error) {
	ps, err := a.tasks().Projects(ctx)
	if err != nil {
		return nil, err
	}
	for i := range ps {
		if ps[i].ID == id {
			return &ps[i], nil
		}
	}
	return nil, nil
}
