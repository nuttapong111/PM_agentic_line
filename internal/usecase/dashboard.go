package usecase

import (
	"context"
	"html"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

// Dashboard page data.

type projectName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Dashboard is the main dashboard payload: followed projects and the selected one's metrics.
// fresh=true skips the stored metrics (the refresh button).
func (a *App) Dashboard(ctx context.Context, want string, fresh bool) (map[string]any, error) {
	ps, err := a.tasks().Projects(ctx)
	if err != nil {
		return nil, err
	}
	pick := func(id string) *domain.Project {
		for i := range ps {
			if ps[i].ID == id {
				return &ps[i]
			}
		}
		return nil
	}
	sel := pick(want)
	if sel == nil {
		sel = pick(a.Store.CurrentProject())
	}
	if sel == nil && len(ps) > 0 {
		sel = &ps[0]
	}
	if sel == nil {
		return map[string]any{"projects": []projectName{}, "metrics": nil}, nil
	}
	m, at, err := a.dashMetrics(ctx, *sel, fresh)
	if err != nil {
		return nil, err
	}
	names := make([]projectName, len(ps))
	for i, p := range ps {
		names[i] = projectName{p.ID, p.Name}
	}
	return map[string]any{
		"projects": names, "selected": sel.ID, "metrics": m,
		"updatedAt": at.UTC().Format("2006-01-02T15:04:05.000Z"),
	}, nil
}

func nonNil[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
}

// DashboardRecords is the risk log, decisions, CRs, milestones and man-day budget of a project.
func (a *App) DashboardRecords(ctx context.Context, want string) (map[string]any, error) {
	if !a.Records.Enabled() {
		return map[string]any{"enabled": false}, nil
	}
	ps, err := a.tasks().Projects(ctx)
	if err != nil {
		return nil, err
	}
	var p *domain.Project
	for i := range ps {
		if ps[i].ID == want {
			p = &ps[i]
		}
	}
	if p == nil && len(ps) > 0 {
		p = &ps[0]
	}
	if p == nil {
		return map[string]any{"enabled": true, "risks": []any{}, "decisions": []any{}, "crs": []any{}, "milestones": []any{}, "budget": nil}, nil
	}
	risks, err := a.records(ctx, domain.KindRisk, domain.ListFilter{ProjectID: p.ID})
	if err != nil {
		return nil, err
	}
	decisions, err := a.records(ctx, domain.KindDecision, domain.ListFilter{ProjectID: p.ID, Limit: 20})
	if err != nil {
		return nil, err
	}
	crs, err := a.records(ctx, domain.KindCR, domain.ListFilter{ProjectID: p.ID})
	if err != nil {
		return nil, err
	}
	ms, err := a.MilestonesFor(ctx, *p, true)
	if err != nil {
		ms = nil
	}
	var budget any
	if b, err := a.BudgetStatus(ctx, *p); err == nil {
		budget = b
	}
	return map[string]any{
		"enabled": true, "risks": nonNil(risks), "decisions": nonNil(decisions), "crs": nonNil(crs),
		"milestones": nonNil(ms), "budget": budget,
	}, nil
}

// HistoryData is the trend data for the dashboard (7–365 days, default 90).
func (a *App) HistoryData(ctx context.Context, projectID string, days int) (port.HistoryResult, error) {
	if days == 0 {
		days = 90
	}
	days = max(7, min(365, days))
	return a.History.History(ctx, projectID, days)
}

// Usage is this month's token count.
func (a *App) Usage() domain.Usage { return a.Store.Snapshot().Usage }

// PreviewPage is the full view of an email / report draft waiting for confirmation. ok=false: gone.
func (a *App) PreviewPage(id string) (string, bool) {
	s := a.Store.Snapshot()
	var pv *domain.Preview
	if p := s.Proactive[id]; p != nil {
		pv = &p.Preview
	} else if s.Pending != nil && s.Pending.ID == id {
		for i := range s.Pending.Writes {
			if s.Pending.Writes[i].Preview.FullHTML != "" {
				pv = &s.Pending.Writes[i].Preview
				break
			}
		}
	}
	if pv == nil || pv.FullHTML == "" {
		return "", false
	}
	head := ""
	if pl := pv.Plan; pl.Kind == domain.PlanEmail {
		to := strings.Join(pl.To, ", ")
		if to == "" {
			to = "(ยังไม่ได้ตั้ง)"
		}
		cc := ""
		if len(pl.Cc) > 0 {
			cc = "<br>สำเนา: " + strings.Join(pl.Cc, ", ")
		}
		head = `<p style="color:#5A6573;font-size:13px">ถึง: ` + to + cc + "<br>เรื่อง: " + strings.ReplaceAll(pl.Subject, "<", "&lt;") + "</p><hr>"
	}
	return `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>ร่าง</title><body style="max-width:720px;margin:auto;padding:20px;background:#fff">` +
		head + pv.FullHTML + `<p style="color:#5A6573;font-size:12px;margin-top:24px">กดส่งจากการ์ดใน LINE</p></body>`, true
}

// OAuthPage is the page shown after Google sign-in.
func (a *App) OAuthPage(msg string, ok bool) string {
	back := a.SettingsURL("")
	color, title := "#A2231B", "เชื่อมไม่สำเร็จ"
	if ok {
		color, title = "#1B6B34", "เชื่อม Google แล้ว"
	}
	return `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Google</title>
<body style="font-family:system-ui,sans-serif;padding:32px;max-width:520px;margin:auto;color:#18202B"><h2 style="color:` + color + `">` + title + `</h2><p>` +
		strings.ReplaceAll(msg, "<", "&lt;") + `</p><p><a href="` + html.EscapeString(back) + `">กลับไปหน้าตั้งค่า</a></p></body>`
}

// The dashboard serves stored metrics instead of asking the task tool on every load:
//   - every 15 minutes RefreshDashboards recomputes each followed project in the background;
//   - results are kept in memory and in Postgres, so a restart or deploy serves them at once;
//   - a load finds them fresh for dashFresh; older ones (up to dashStale) are shown at once while
//     a background refresh runs; past dashStale, or with fresh=true (the refresh button), the load
//     computes them now;
//   - a confirmed write or a Plane webhook refreshes that project right away.
const (
	dashFresh = 15 * time.Minute
	dashStale = 24 * time.Hour
	dashEvery = 15 // minutes between background refreshes
)

type dashCache struct {
	mu         sync.Mutex
	entries    map[string]dashEntry // by workspace + project ID
	refreshing map[string]bool
	loaded     map[string]bool // workspaces read back from the database
}

type dashEntry struct {
	at time.Time
	m  domain.ProjectMetrics
}

func dashKey(ws, projectID string) string { return ws + "|" + projectID }

// loadSavedDash fills the memory cache from the database once per workspace.
func (a *App) loadSavedDash(ctx context.Context, ws string) {
	c := &a.dash
	c.mu.Lock()
	done := c.loaded[ws]
	if c.loaded == nil {
		c.loaded = map[string]bool{}
	}
	c.loaded[ws] = true
	c.mu.Unlock()
	if done {
		return
	}
	saved, err := a.History.LoadDashboard(ctx, ws)
	if err != nil {
		slog.Warn("saved dashboard unreadable", "err", err)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]dashEntry{}
	}
	for id, pt := range saved {
		k := dashKey(ws, id)
		if e, ok := c.entries[k]; !ok || e.at.Before(pt.At) {
			c.entries[k] = dashEntry{pt.At, pt.Metrics}
		}
	}
}

func (a *App) dashMetrics(ctx context.Context, p domain.Project, fresh bool) (domain.ProjectMetrics, time.Time, error) {
	if fresh {
		return a.computeDash(port.WithFresh(ctx), p)
	}
	ws := a.ws()
	a.loadSavedDash(ctx, ws)
	c := &a.dash
	c.mu.Lock()
	e, ok := c.entries[dashKey(ws, p.ID)]
	age := a.Now().Sub(e.at)
	c.mu.Unlock()
	if ok && age < dashFresh {
		return e.m, e.at, nil
	}
	if ok && age < dashStale {
		a.refreshDashAsync(p)
		return e.m, e.at, nil
	}
	return a.computeDash(ctx, p)
}

// refreshDashAsync recomputes a project in the background unless that is already running.
func (a *App) refreshDashAsync(p domain.Project) {
	k := dashKey(a.ws(), p.ID)
	c := &a.dash
	c.mu.Lock()
	if c.refreshing[k] {
		c.mu.Unlock()
		return
	}
	if c.refreshing == nil {
		c.refreshing = map[string]bool{}
	}
	c.refreshing[k] = true
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.refreshing, k)
			c.mu.Unlock()
		}()
		bg, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if _, _, err := a.computeDash(bg, p); err != nil {
			slog.Warn("dashboard refresh failed", "project", p.Identifier, "err", err)
		}
	}()
}

func (a *App) computeDash(ctx context.Context, p domain.Project) (domain.ProjectMetrics, time.Time, error) {
	ws := a.ws()
	m, err := a.ProjectMetrics(ctx, p)
	if err != nil {
		return m, time.Time{}, err
	}
	now := a.Now()
	c := &a.dash
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]dashEntry{}
	}
	c.entries[dashKey(ws, p.ID)] = dashEntry{now, m}
	c.mu.Unlock()
	go func() {
		bg := context.Background()
		if err := a.History.SaveDashboard(bg, ws, m, now); err != nil {
			slog.Error("dashboard save failed", "err", err)
		}
		if err := a.Snapshot(bg, m); err != nil { // today's history point stays fresh
			slog.Error("snapshot failed", "err", err)
		}
	}()
	return m, now, nil
}

// RefreshDashboards recomputes every followed project, one after another (the scheduler runs it every 15 minutes).
func (a *App) RefreshDashboards(ctx context.Context) {
	if !a.dashMu.TryLock() {
		return // the last round is still running
	}
	defer a.dashMu.Unlock()
	ps, err := a.tasks().Projects(ctx)
	if err != nil {
		slog.Warn("dashboard refresh failed", "err", err)
		return
	}
	for _, p := range ps {
		if ctx.Err() != nil {
			return
		}
		if _, _, err := a.computeDash(ctx, p); err != nil {
			slog.Warn("dashboard refresh failed", "project", p.Identifier, "err", err)
		}
	}
}

// ForgetDashboard refreshes a project's dashboard after a write so the next load is current.
func (a *App) ForgetDashboard(projectID string) {
	ws := a.ws()
	a.dash.mu.Lock()
	delete(a.dash.entries, dashKey(ws, projectID))
	a.dash.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if p, err := a.findProject(ctx, projectID); err == nil && p != nil {
			a.refreshDashAsync(*p)
		}
	}()
}
