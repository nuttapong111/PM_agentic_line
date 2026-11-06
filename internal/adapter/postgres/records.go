package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nuttapong111/pm_agentic_line/internal/domain"
)

// Records implements port.RecordRepo on pm_records.
type Records struct{ db *DB }

func (db *DB) Records() *Records { return &Records{db} }

func (r *Records) Enabled() bool { return true }

const recCols = `id, ws, kind, code, project_id, status, data, due_at, created_at, updated_at`

func scanRecord(row pgx.Row) (domain.Record, error) {
	var x domain.Record
	var data []byte
	err := row.Scan(&x.ID, &x.WS, &x.Kind, &x.Code, &x.ProjectID, &x.Status, &data, &x.DueAt, &x.CreatedAt, &x.UpdatedAt)
	x.Data = json.RawMessage(data)
	return x, err
}

func collect(rows pgx.Rows) ([]domain.Record, error) {
	defer rows.Close()
	out := []domain.Record{}
	for rows.Next() {
		x, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r *Records) Add(ctx context.Context, ws string, kind domain.RecordKind, projectID *string, data any, status string, dueAt *time.Time) (domain.Record, error) {
	var code *string
	if pre, ok := domain.RecordPrefix[kind]; ok {
		var n int
		if err := r.db.Pool.QueryRow(ctx, `select count(*)::int + 1 from pm_records where ws = $1 and kind = $2`, ws, string(kind)).Scan(&n); err != nil {
			return domain.Record{}, err
		}
		c := fmt.Sprintf("%s-%02d", pre, n)
		code = &c
	}
	if status == "" {
		status = "open"
	}
	b, err := json.Marshal(data)
	if err != nil {
		return domain.Record{}, err
	}
	return scanRecord(r.db.Pool.QueryRow(ctx, `insert into pm_records (ws, kind, code, project_id, status, data, due_at)
    values ($1, $2, $3, $4, $5, $6::jsonb, $7) returning `+recCols, ws, string(kind), code, projectID, status, string(b), dueAt))
}

func (r *Records) List(ctx context.Context, ws string, kinds []domain.RecordKind, f domain.ListFilter) ([]domain.Record, error) {
	ks := make([]string, len(kinds))
	for i, k := range kinds {
		ks[i] = string(k)
	}
	q := `select ` + recCols + ` from pm_records where ws = $1 and kind = any($2)`
	args := []any{ws, ks}
	if f.ProjectID != "" {
		args = append(args, f.ProjectID)
		q += fmt.Sprintf(` and (project_id = $%d or project_id is null)`, len(args))
	}
	if f.Status != nil {
		args = append(args, f.Status)
		q += fmt.Sprintf(` and status = any($%d)`, len(args))
	}
	limit := f.Limit
	if limit == 0 {
		limit = 100
	}
	args = append(args, limit)
	q += fmt.Sprintf(` order by coalesce(due_at, created_at) desc limit $%d`, len(args))
	rows, err := r.db.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

func one(x domain.Record, err error) (*domain.Record, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &x, nil
}

func (r *Records) Get(ctx context.Context, ws string, id int64) (*domain.Record, error) {
	return one(scanRecord(r.db.Pool.QueryRow(ctx, `select `+recCols+` from pm_records where id = $1 and ws = $2`, id, ws)))
}

func (r *Records) GetByCode(ctx context.Context, ws, code string) (*domain.Record, error) {
	return one(scanRecord(r.db.Pool.QueryRow(ctx, `select `+recCols+` from pm_records where upper(code) = $1 and ws = $2 order by id desc limit 1`, strings.ToUpper(code), ws)))
}

func (r *Records) Update(ctx context.Context, ws string, id int64, p domain.RecordPatch) (*domain.Record, error) {
	data := p.Data
	if data == nil {
		data = map[string]any{}
	}
	b, _ := json.Marshal(data)
	return one(scanRecord(r.db.Pool.QueryRow(ctx, `update pm_records set
      status = coalesce($3, status),
      data = data || $4::jsonb,
      due_at = case when $5 then $6 else due_at end,
      updated_at = now()
    where id = $1 and ws = $2 returning `+recCols, id, ws, p.Status, string(b), p.SetDueAt, p.DueAt)))
}

func (r *Records) DueReminders(ctx context.Context) ([]domain.Record, error) {
	rows, err := r.db.Pool.Query(ctx, `select `+recCols+` from pm_records where kind = 'reminder' and status = 'open' and due_at <= now() order by due_at limit 5`)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}
