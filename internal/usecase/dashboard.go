package usecase

import (
	"context"
	"html"
	"log/slog"
	"strings"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

// Dashboard page data.

type projectName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Dashboard is the main dashboard payload: followed projects and the selected one's metrics.
func (a *App) Dashboard(ctx context.Context, want string) (map[string]any, error) {
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
	m, err := a.ProjectMetrics(ctx, *sel)
	if err != nil {
		return nil, err
	}
	go func() { // today's point stays fresh
		if err := a.Snapshot(context.Background(), m); err != nil {
			slog.Error("snapshot failed", "err", err)
		}
	}()
	names := make([]projectName, len(ps))
	for i, p := range ps {
		names[i] = projectName{p.ID, p.Name}
	}
	return map[string]any{
		"projects": names, "selected": sel.ID, "metrics": m,
		"updatedAt": a.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
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
