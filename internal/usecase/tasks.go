package usecase

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/nuttapong111/pm_agentic_line/internal/config"
	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

// PlaneFactory builds a Plane client for a server, workspace, token and follow list.
type PlaneFactory func(baseURL, workspace, token string, follow []string) port.TaskProvider

// Tasks picks the task provider from Settings (or env on first run). Call Reset after Settings
// change; the next Current builds the new one.
type Tasks struct {
	cfg     config.Config
	store   *Store
	secrets port.Secrets
	history port.HistoryRepo
	plane   PlaneFactory
	builtin port.TaskProvider // nil without a database

	mu         sync.Mutex
	cur        port.TaskProvider
	backfilled bool
}

func NewTasks(cfg config.Config, store *Store, secrets port.Secrets, history port.HistoryRepo, plane PlaneFactory, builtin port.TaskProvider) *Tasks {
	return &Tasks{cfg: cfg, store: store, secrets: secrets, history: history, plane: plane, builtin: builtin}
}

func (t *Tasks) build() port.TaskProvider {
	c := t.store.Connections().Task
	switch c.Provider {
	case domain.ProviderPlane:
		if c.Plane != nil {
			if token, ok := t.secrets.Decrypt(c.Plane.TokenEnc); ok {
				return t.plane(c.Plane.BaseURL, c.Plane.Workspace, token, c.Plane.Projects)
			}
		}
	case domain.ProviderBuiltin:
		if t.builtin != nil {
			return t.builtin
		}
	case domain.ProviderEnv:
		if t.cfg.EnvPlaneSet() {
			return t.plane(t.cfg.PlaneBaseURL, t.cfg.PlaneWorkspace, t.cfg.PlaneAPIKey, t.cfg.PlaneProjects)
		}
	}
	return NoProvider{}
}

// Current returns the configured provider, building it on first use.
func (t *Tasks) Current() port.TaskProvider {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cur == nil {
		t.cur = t.build()
		if t.history.Enabled() && !t.backfilled && t.cur.Kind() != port.KindNone {
			t.backfilled = true // rows written before workspaces were tracked belong to the first one
			ws := t.cur.WorkspaceKey()
			go func() {
				if err := t.history.BackfillWorkspace(context.Background(), ws); err != nil {
					slog.Error("ws backfill failed", "err", err)
				}
			}()
		}
	}
	return t.cur
}

// Workspace is the key new rows are tagged with.
func (t *Tasks) Workspace() string { return t.Current().WorkspaceKey() }

func (t *Tasks) Reset() {
	t.mu.Lock()
	t.cur = nil
	t.mu.Unlock()
}

// PlaneFrom builds a Plane client (Settings uses it to test a connection before saving).
func (t *Tasks) PlaneFrom(baseURL, workspace, token string, follow []string) port.TaskProvider {
	return t.plane(strings.TrimSuffix(baseURL, "/"), workspace, token, follow)
}

// NoProvider is the placeholder when nothing is connected: reads return empty, writes explain what to do.
type NoProvider struct{}

func (NoProvider) Kind() string         { return port.KindNone }
func (NoProvider) WorkspaceKey() string { return "none" }
func (NoProvider) Me(context.Context) (domain.Member, error) {
	return domain.Member{}, domain.ErrTaskNotConnected
}
func (NoProvider) AllProjects(context.Context) ([]domain.Project, error) {
	return []domain.Project{}, nil
}
func (NoProvider) Projects(context.Context) ([]domain.Project, error) { return []domain.Project{}, nil }
func (NoProvider) States(context.Context, string) ([]domain.State, error) {
	return []domain.State{}, nil
}
func (NoProvider) Members(context.Context, string) ([]domain.Member, error) {
	return []domain.Member{}, nil
}
func (NoProvider) Labels(context.Context, string) ([]domain.Label, error) {
	return []domain.Label{}, nil
}
func (NoProvider) Cycles(context.Context, string) ([]domain.Cycle, error) {
	return []domain.Cycle{}, nil
}
func (NoProvider) Modules(context.Context, string) ([]domain.Module, error) {
	return []domain.Module{}, nil
}
func (NoProvider) CycleLinks(context.Context, string, string) ([]domain.CycleLink, error) {
	return []domain.CycleLink{}, nil
}
func (NoProvider) ModuleItemIDs(context.Context, string, string) ([]string, error) {
	return []string{}, nil
}
func (NoProvider) WorkItems(context.Context, domain.Project) ([]domain.WorkItem, error) {
	return []domain.WorkItem{}, nil
}
func (NoProvider) WorkItemByKey(context.Context, string) (domain.WorkItem, error) {
	return domain.WorkItem{}, domain.ErrTaskNotConnected
}
func (NoProvider) CreateWorkItem(context.Context, domain.Project, domain.WorkItemWrite) (domain.WorkItem, error) {
	return domain.WorkItem{}, domain.ErrTaskNotConnected
}
func (NoProvider) UpdateWorkItem(context.Context, domain.Project, string, domain.WorkItemWrite) (domain.WorkItem, error) {
	return domain.WorkItem{}, domain.ErrTaskNotConnected
}
func (NoProvider) AddToCycle(context.Context, string, string, []string) error {
	return domain.ErrTaskNotConnected
}
func (NoProvider) NewProject(context.Context, domain.ProjectWrite) (domain.Project, error) {
	return domain.Project{}, domain.ErrTaskNotConnected
}
func (NoProvider) WorkspaceMembers(context.Context) ([]domain.Member, error) {
	return nil, domain.ErrTaskNotConnected
}
func (NoProvider) AddProjectMember(context.Context, string, string, int) error {
	return domain.ErrTaskNotConnected
}
func (NoProvider) MembersURL(domain.Project) string { return "" }
func (NoProvider) CreateCycle(context.Context, string, domain.CycleWrite) (domain.Cycle, error) {
	return domain.Cycle{}, domain.ErrTaskNotConnected
}
func (NoProvider) UpdateModule(context.Context, string, string, domain.ModuleWrite) (domain.Module, error) {
	return domain.Module{}, domain.ErrTaskNotConnected
}
func (NoProvider) AddToModule(context.Context, string, string, []string) error {
	return domain.ErrTaskNotConnected
}
func (NoProvider) AddComment(context.Context, domain.Project, string, string) error {
	return domain.ErrTaskNotConnected
}
func (NoProvider) ItemURL(domain.WorkItem) string   { return "" }
func (NoProvider) KeyURL(string) string             { return "" }
func (NoProvider) ProjectURL(domain.Project) string { return "" }
