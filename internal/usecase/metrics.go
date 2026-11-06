package usecase

import (
	"context"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
)

// LoadData fetches what metrics and tools need about one project.
func (a *App) LoadData(ctx context.Context, p domain.Project) (*domain.ProjectData, error) {
	t := a.tasks()
	items, err := t.WorkItems(ctx, p)
	if err != nil {
		return nil, err
	}
	states, err := t.States(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	members, err := t.Members(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	labels, err := t.Labels(ctx, p.ID)
	if err != nil {
		labels = nil
	}
	return domain.NewProjectData(p, items, states, members, labels), nil
}

func (a *App) currentCycle(ctx context.Context, projectID string) *domain.Cycle {
	cycles, err := a.tasks().Cycles(ctx, projectID)
	if err != nil {
		return nil
	}
	return domain.CurrentCycle(cycles, a.today())
}

// CycleMetrics computes a cycle's progress (the current one when c is nil).
func (a *App) CycleMetrics(ctx context.Context, d *domain.ProjectData, c *domain.Cycle) *domain.CycleMetrics {
	if c == nil {
		c = a.currentCycle(ctx, d.Project.ID)
	}
	if c == nil {
		return nil
	}
	links, err := a.tasks().CycleLinks(ctx, d.Project.ID, c.ID)
	if err != nil {
		links = nil
	}
	return domain.ComputeCycle(d, *c, links, a.Now(), a.Loc)
}

// ProjectMetrics computes the project overview.
func (a *App) ProjectMetrics(ctx context.Context, p domain.Project) (domain.ProjectMetrics, error) {
	d, err := a.LoadData(ctx, p)
	if err != nil {
		return domain.ProjectMetrics{}, err
	}
	return a.metricsFor(ctx, d), nil
}

func (a *App) metricsFor(ctx context.Context, d *domain.ProjectData) domain.ProjectMetrics {
	t := a.tasks()
	p := d.Project
	prefs := a.Store.Prefs()
	in := domain.MetricsInput{
		Now: a.Now(), Loc: a.Loc, StaleDays: prefs.StaleDays, BugLabel: prefs.BugLabel,
		ItemURL: t.ItemURL, URL: t.ProjectURL(p),
	}
	if c := a.currentCycle(ctx, p.ID); c != nil {
		in.Cycle = c
		in.CycleRows, _ = t.CycleLinks(ctx, p.ID, c.ID)
	}
	mods, err := t.Modules(ctx, p.ID)
	if err == nil {
		for _, m := range mods {
			ids, err := t.ModuleItemIDs(ctx, p.ID, m.ID)
			if err != nil {
				ids = nil
			}
			in.Modules = append(in.Modules, domain.ModuleItems{Module: m, ItemIDs: ids})
		}
	}
	return domain.ComputeMetrics(d, in)
}

// view is an item as the owner sees it.
func (a *App) view(d *domain.ProjectData, it domain.WorkItem) domain.ItemView {
	return d.View(it, a.today(), a.tasks().ItemURL(it))
}

// Snapshot stores today's metrics point.
func (a *App) Snapshot(ctx context.Context, m domain.ProjectMetrics) error {
	return a.History.Snapshot(ctx, a.ws(), m)
}
