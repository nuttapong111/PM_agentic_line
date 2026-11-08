package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

// History implements port.HistoryRepo.
type History struct{ db *DB }

func (db *DB) History() *History { return &History{db} }

func (h *History) Enabled() bool { return true }

func (h *History) LogActivity(ctx context.Context, ws, kind, summary string, projectID *string, detail any) {
	var d *string
	if detail != nil {
		b, err := json.Marshal(detail)
		if err == nil {
			s := string(b)
			d = &s
		}
	}
	if _, err := h.db.Pool.Exec(ctx, `insert into activity_log (kind, project_id, summary, detail, ws) values ($1, $2, $3, $4::jsonb, $5)`,
		kind, projectID, summary, d, ws); err != nil {
		slog.Error("db: activity log failed", "err", err)
	}
}

func (h *History) LogUsage(ctx context.Context, model string, input, output, cacheRead int64) {
	if _, err := h.db.Pool.Exec(ctx, `insert into usage_log (model, input_tokens, output_tokens, cache_read_tokens) values ($1, $2, $3, $4)`,
		model, input, output, cacheRead); err != nil {
		slog.Error("db: usage log failed", "err", err)
	}
}

func (h *History) Snapshot(ctx context.Context, ws string, m domain.ProjectMetrics) error {
	var name *string
	var pct, total, done *int
	if c := m.Cycle; c != nil {
		name, pct, total, done = &c.Name, &c.Percent, &c.Total, &c.Done
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = h.db.Pool.Exec(ctx, `insert into metrics_daily (day, project_id, project_name, open, overdue, blocked, stale, done_this_week, unassigned, urgent_bugs,
      cycle_name, cycle_percent, cycle_total, cycle_done, metrics, ws)
    values ($1::date, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb, $16)
    on conflict (day, project_id) do update set
      project_name = excluded.project_name, open = excluded.open, overdue = excluded.overdue, blocked = excluded.blocked,
      stale = excluded.stale, done_this_week = excluded.done_this_week, unassigned = excluded.unassigned,
      urgent_bugs = excluded.urgent_bugs, cycle_name = excluded.cycle_name, cycle_percent = excluded.cycle_percent,
      cycle_total = excluded.cycle_total, cycle_done = excluded.cycle_done, metrics = excluded.metrics, ws = excluded.ws`,
		m.Today, m.Project.ID, m.Project.Name, m.Counts.Open, m.Counts.Overdue, m.Counts.Blocked, len(m.Stale),
		m.Counts.DoneThisWeek, m.Counts.Unassigned, m.Counts.UrgentBugs, name, pct, total, done, string(b), ws)
	return err
}

const pointCols = `to_char(day, 'YYYY-MM-DD'), open, overdue, blocked, stale, done_this_week, unassigned, urgent_bugs, cycle_name, cycle_percent`

func scanPoint(row pgx.Row) (port.HistoryPoint, error) {
	var p port.HistoryPoint
	err := row.Scan(&p.Day, &p.Open, &p.Overdue, &p.Blocked, &p.Stale, &p.DoneThisWeek, &p.Unassigned, &p.UrgentBugs, &p.CycleName, &p.CyclePercent)
	return p, err
}

func (h *History) History(ctx context.Context, projectID string, days int) (port.HistoryResult, error) {
	res := port.HistoryResult{Enabled: true, Points: []port.HistoryPoint{}, Activity: []port.ActivityRow{}, Usage: []port.UsageRow{}}
	rows, err := h.db.Pool.Query(ctx, `select `+pointCols+` from metrics_daily where project_id = $1 and day >= current_date - $2::int order by day`, projectID, days)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		p, err := scanPoint(rows)
		if err != nil {
			rows.Close()
			return res, err
		}
		res.Points = append(res.Points, p)
	}
	rows.Close()
	rows, err = h.db.Pool.Query(ctx, `select at, kind, summary from activity_log where project_id = $1 or project_id is null order by at desc limit 30`, projectID)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var a port.ActivityRow
		if err := rows.Scan(&a.At, &a.Kind, &a.Summary); err != nil {
			rows.Close()
			return res, err
		}
		res.Activity = append(res.Activity, a)
	}
	rows.Close()
	rows, err = h.db.Pool.Query(ctx, `select to_char(date_trunc('month', at), 'YYYY-MM'), count(*)::int,
      coalesce(sum(input_tokens),0)::bigint, coalesce(sum(output_tokens),0)::bigint, coalesce(sum(cache_read_tokens),0)::bigint
    from usage_log group by 1 order by 1 desc limit 6`)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		var u port.UsageRow
		if err := rows.Scan(&u.Month, &u.Calls, &u.Input, &u.Output, &u.CacheRead); err != nil {
			return res, err
		}
		res.Usage = append(res.Usage, u)
	}
	return res, rows.Err()
}

func (h *History) MetricsAround(ctx context.Context, projectID string, daysAgo int) (*port.HistoryPoint, error) {
	p, err := scanPoint(h.db.Pool.QueryRow(ctx, `select `+pointCols+` from metrics_daily where project_id = $1 and day <= current_date - $2::int order by day desc limit 1`, projectID, daysAgo))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (h *History) DataSummary(ctx context.Context) ([]port.WorkspaceSummary, error) {
	rows, err := h.db.Pool.Query(ctx, `with w as (
      select ws from metrics_daily where ws is not null union select ws from activity_log where ws is not null union select ws from pm_records)
    select w.ws,
      (select to_char(min(day),'YYYY-MM-DD') from metrics_daily m where m.ws = w.ws),
      (select count(*)::int from metrics_daily m where m.ws = w.ws),
      (select count(*)::int from activity_log a where a.ws = w.ws),
      (select count(*)::int from pm_records r where r.ws = w.ws and r.kind in ('risk','decision','cr')),
      (select count(*)::int from pm_records r where r.ws = w.ws and r.kind = 'milestone')
    from w order by 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []port.WorkspaceSummary{}
	for rows.Next() {
		var s port.WorkspaceSummary
		if err := rows.Scan(&s.WS, &s.Since, &s.Points, &s.Activity, &s.Records, &s.Milestones); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (h *History) table(ctx context.Context, q string, args ...any) (port.Table, error) {
	rows, err := h.db.Pool.Query(ctx, q, args...)
	if err != nil {
		return port.Table{}, err
	}
	defer rows.Close()
	t := port.Table{Rows: []map[string]any{}}
	for _, f := range rows.FieldDescriptions() {
		t.Columns = append(t.Columns, f.Name)
	}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return t, err
		}
		m := make(map[string]any, len(vals))
		for i, v := range vals {
			m[t.Columns[i]] = v
		}
		t.Rows = append(t.Rows, m)
	}
	return t, rows.Err()
}

func (h *History) Export(ctx context.Context, ws string) (port.ExportData, error) {
	var d port.ExportData
	var err error
	if d.Metrics, err = h.table(ctx, `select to_char(day,'YYYY-MM-DD') as day, project_id, project_name, open, overdue, blocked, stale, done_this_week, unassigned, urgent_bugs, cycle_name, cycle_percent from metrics_daily where ws = $1 order by day`, ws); err != nil {
		return d, err
	}
	if d.Activity, err = h.table(ctx, `select at, kind, project_id, summary from activity_log where ws = $1 order by at`, ws); err != nil {
		return d, err
	}
	d.Records, err = h.table(ctx, `select id, kind, code, project_id, status, data, due_at, created_at, updated_at from pm_records where ws = $1 order by id`, ws)
	return d, err
}

func (h *History) DeleteWorkspace(ctx context.Context, ws *string) error {
	var qs []string
	var args []any
	if ws == nil {
		qs = []string{`truncate metrics_daily, activity_log, usage_log, pm_records, dashboard_cache`, `delete from app_state`, `drop table if exists bt_comments, bt_items, bt_projects`}
	} else {
		qs = []string{`delete from metrics_daily where ws = $1`, `delete from activity_log where ws = $1`, `delete from pm_records where ws = $1`, `delete from dashboard_cache where ws = $1`}
		args = []any{*ws}
	}
	for _, q := range qs {
		if _, err := h.db.Pool.Exec(ctx, q, args...); err != nil {
			return err
		}
	}
	return nil
}

func (h *History) UsageThisMonth(ctx context.Context) (*int, error) {
	var n int
	if err := h.db.Pool.QueryRow(ctx, `select count(*)::int from usage_log where at >= date_trunc('month', now())`).Scan(&n); err != nil {
		return nil, err
	}
	return &n, nil
}

func (h *History) BackfillWorkspace(ctx context.Context, ws string) error {
	if _, err := h.db.Pool.Exec(ctx, `update activity_log set ws = $1 where ws is null`, ws); err != nil {
		return err
	}
	_, err := h.db.Pool.Exec(ctx, `update metrics_daily set ws = $1 where ws is null`, ws)
	return err
}

func (h *History) SaveDashboard(ctx context.Context, ws string, m domain.ProjectMetrics, at time.Time) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = h.db.Pool.Exec(ctx, `insert into dashboard_cache (ws, project_id, metrics, updated_at) values ($1, $2, $3, $4)
on conflict (ws, project_id) do update set metrics = excluded.metrics, updated_at = excluded.updated_at`, ws, m.Project.ID, b, at)
	return err
}

func (h *History) LoadDashboard(ctx context.Context, ws string) (map[string]port.DashboardPoint, error) {
	rows, err := h.db.Pool.Query(ctx, `select project_id, metrics, updated_at from dashboard_cache where ws = $1`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]port.DashboardPoint{}
	for rows.Next() {
		var id string
		var raw []byte
		var p port.DashboardPoint
		if err := rows.Scan(&id, &raw, &p.At); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &p.Metrics) != nil {
			continue // written by an older version; the next refresh replaces it
		}
		out[id] = p
	}
	return out, rows.Err()
}
