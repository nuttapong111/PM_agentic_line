package usecase

import (
	"context"
	"log/slog"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

// BuildDigest is the morning digest: computed from task data and today's meetings (no AI call).
func (a *App) BuildDigest(ctx context.Context) port.Message {
	ps, err := a.tasks().Projects(ctx)
	if err != nil {
		slog.Error("digest: projects failed", "err", err)
	}
	metrics := []domain.ProjectMetrics{}
	for _, p := range ps {
		m, err := a.ProjectMetrics(ctx, p)
		if err != nil {
			slog.Error("digest: project failed", "project", p.Identifier, "err", err)
			continue
		}
		metrics = append(metrics, m)
		_ = a.Snapshot(ctx, m)
	}
	today := a.today()
	label := domain.ThaiDaysFull[domain.Weekday(today)] + " " + domain.ThaiDate(today, false)
	meetings := []string{}
	if a.Google.Connected() {
		evs, err := a.Google.TodaysMeetings(ctx)
		if err != nil {
			slog.Error("digest: calendar failed", "err", err)
		}
		for i, e := range evs {
			if i == 4 {
				break
			}
			s := domain.HM(e.StartTime(), a.Loc) + " " + e.Summary
			if e.MeetURL != "" {
				s += " · Meet"
			}
			meetings = append(meetings, s)
		}
	}
	return a.View.Digest(metrics, label, a.DashboardURL(""), meetings, a.mailHighlights(ctx))
}

// SnapshotAll saves today's metrics point for every project (run nightly).
func (a *App) SnapshotAll(ctx context.Context) {
	ps, err := a.tasks().Projects(ctx)
	if err != nil {
		slog.Error("snapshot failed", "err", err)
		return
	}
	for _, p := range ps {
		m, err := a.ProjectMetrics(ctx, p)
		if err == nil {
			err = a.Snapshot(ctx, m)
		}
		if err != nil {
			slog.Error("snapshot failed", "project", p.Identifier, "err", err)
		}
	}
}
