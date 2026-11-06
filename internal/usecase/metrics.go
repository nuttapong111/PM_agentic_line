package usecase

import (
	"context"
	"sync"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
)

// LoadData fetches what metrics and tools need about one project.
func (a *App) LoadData(ctx context.Context, p domain.Project) (*domain.ProjectData, error) {
	t := a.tasks()
	// The four lists are independent: fetch them together.
	var (
		wg                   sync.WaitGroup
		items                []domain.WorkItem
		states               []domain.State
		members              []domain.Member
		labels               []domain.Label
		errItems, errS, errM error
	)
	wg.Add(4)
	go func() { defer wg.Done(); items, errItems = t.WorkItems(ctx, p) }()
	go func() { defer wg.Done(); states, errS = t.States(ctx, p.ID) }()
	go func() { defer wg.Done(); members, errM = t.Members(ctx, p.ID) }()
	go func() {
		defer wg.Done()
		var err error
		if labels, err = t.Labels(ctx, p.ID); err != nil {
			labels = nil
		}
	}()
	wg.Wait()
	for _, err := range []error{errItems, errS, errM} {
		if err != nil {
			return nil, err
		}
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
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if c := a.currentCycle(ctx, p.ID); c != nil {
			in.Cycle = c
			in.CycleRows, _ = t.CycleLinks(ctx, p.ID, c.ID)
		}
	}()
	go func() {
		defer wg.Done()
		mods, err := t.Modules(ctx, p.ID)
		if err != nil {
			return
		}
		// Each module's items is its own request: a few at a time, results kept in module order.
		out := make([]domain.ModuleItems, len(mods))
		sem := make(chan struct{}, 4)
		var mw sync.WaitGroup
		for i, m := range mods {
			mw.Add(1)
			sem <- struct{}{}
			go func(i int, m domain.Module) {
				defer func() { <-sem; mw.Done() }()
				ids, err := t.ModuleItemIDs(ctx, p.ID, m.ID)
				if err != nil {
					ids = nil
				}
				out[i] = domain.ModuleItems{Module: m, ItemIDs: ids}
			}(i, m)
		}
		mw.Wait()
		in.Modules = out
	}()
	wg.Wait()
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
