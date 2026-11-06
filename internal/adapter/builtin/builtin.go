// Package builtin is the task list stored in Postgres: the fallback when there is no external
// task tool. Fixed states, members = nicknames from Settings, no cycles or modules.
package builtin

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nuttapong111/pm_agentic_line/internal/domain"
)

var states = []domain.State{
	{ID: "todo", Name: "Todo", Group: domain.GroupUnstarted},
	{ID: "in_progress", Name: "In Progress", Group: domain.GroupStarted},
	{ID: "blocked", Name: "Blocked", Group: domain.GroupStarted},
	{ID: "done", Name: "Done", Group: domain.GroupCompleted},
	{ID: "cancelled", Name: "Cancelled", Group: domain.GroupCancelled},
}

var me = domain.Member{ID: "me", DisplayName: "ฉัน"}

type Provider struct {
	pool      *pgxpool.Pool
	publicURL string
	nicknames func() map[string]string
}

func New(pool *pgxpool.Pool, publicURL string, nicknames func() map[string]string) *Provider {
	return &Provider{pool: pool, publicURL: publicURL, nicknames: nicknames}
}

func (p *Provider) Kind() string         { return "builtin" }
func (p *Provider) WorkspaceKey() string { return "builtin" }

func (p *Provider) Migrate(ctx context.Context) error {
	for _, q := range []string{
		`create table if not exists bt_projects (id text primary key, identifier text unique not null, name text not null, created_at timestamptz not null default now())`,
		`create table if not exists bt_items (
    id text primary key, project_id text not null references bt_projects(id) on delete cascade, seq int not null,
    name text not null, description_html text, state text not null default 'todo', assignees text[] not null default '{}',
    priority text not null default 'none', start_date date, target_date date, completed_at timestamptz,
    parent_id text, created_at timestamptz not null default now(), updated_at timestamptz not null default now(),
    unique (project_id, seq))`,
		`create table if not exists bt_comments (id bigserial primary key, item_id text not null references bt_items(id) on delete cascade, html text not null, created_at timestamptz not null default now())`,
	} {
		if _, err := p.pool.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

var nonIdent = regexp.MustCompile(`[^A-Z0-9]`)

func (p *Provider) CreateProject(ctx context.Context, name, identifier string) (domain.Project, error) {
	ident := domain.Truncate(nonIdent.ReplaceAllString(strings.ToUpper(identifier), ""), 12)
	if ident == "" {
		return domain.Project{}, domain.UserError("รหัสโปรเจกต์ต้องเป็นตัวอักษรภาษาอังกฤษหรือตัวเลข")
	}
	id := uuid()
	if _, err := p.pool.Exec(ctx, `insert into bt_projects (id, identifier, name) values ($1, $2, $3)`, id, ident, name); err != nil {
		return domain.Project{}, err
	}
	return domain.Project{ID: id, Name: name, Identifier: ident}, nil
}

func (p *Provider) Me(context.Context) (domain.Member, error) { return me, nil }

func (p *Provider) AllProjects(ctx context.Context) ([]domain.Project, error) {
	rows, err := p.pool.Query(ctx, `select id, name, identifier from bt_projects order by created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Project{}
	for rows.Next() {
		var x domain.Project
		if err := rows.Scan(&x.ID, &x.Name, &x.Identifier); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (p *Provider) Projects(ctx context.Context) ([]domain.Project, error) { return p.AllProjects(ctx) }
func (p *Provider) States(context.Context, string) ([]domain.State, error) { return states, nil }

func (p *Provider) Members(context.Context, string) ([]domain.Member, error) {
	out := []domain.Member{me}
	nick := p.nicknames()
	names := make([]string, 0, len(nick))
	for n := range nick {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		out = append(out, domain.Member{ID: n, DisplayName: n})
	}
	return out, nil
}

func (p *Provider) Labels(context.Context, string) ([]domain.Label, error) {
	return []domain.Label{}, nil
}
func (p *Provider) Cycles(context.Context, string) ([]domain.Cycle, error) {
	return []domain.Cycle{}, nil
}
func (p *Provider) Modules(context.Context, string) ([]domain.Module, error) {
	return []domain.Module{}, nil
}
func (p *Provider) CycleLinks(context.Context, string, string) ([]domain.CycleLink, error) {
	return []domain.CycleLink{}, nil
}
func (p *Provider) ModuleItemIDs(context.Context, string, string) ([]string, error) {
	return []string{}, nil
}

const cols = `id, project_id, seq, name, state, assignees, priority, to_char(start_date,'YYYY-MM-DD'), to_char(target_date,'YYYY-MM-DD'), completed_at, created_at, updated_at, parent_id`

func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z07:00") }

func scan(row pgx.Row, proj domain.Project) (domain.WorkItem, error) {
	var it domain.WorkItem
	var seq int
	var completed *time.Time
	var created, updated time.Time
	err := row.Scan(&it.ID, &it.ProjectID, &seq, &it.Name, &it.StateID, &it.AssigneeIDs, &it.Priority, &it.StartDate, &it.TargetDate, &completed, &created, &updated, &it.ParentID)
	if err != nil {
		return it, err
	}
	it.Key = fmt.Sprintf("%s-%d", proj.Identifier, seq)
	it.LabelIDs = []string{}
	if it.AssigneeIDs == nil {
		it.AssigneeIDs = []string{}
	}
	if completed != nil {
		it.CompletedAt = domain.Ptr(iso(*completed))
	}
	it.CreatedAt, it.UpdatedAt = iso(created), iso(updated)
	return it, nil
}

func (p *Provider) WorkItems(ctx context.Context, proj domain.Project) ([]domain.WorkItem, error) {
	rows, err := p.pool.Query(ctx, `select `+cols+` from bt_items where project_id = $1 order by seq`, proj.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.WorkItem{}
	for rows.Next() {
		it, err := scan(rows, proj)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (p *Provider) WorkItemByKey(ctx context.Context, key string) (domain.WorkItem, error) {
	ident, seq, err := domain.SplitKey(key)
	if err != nil {
		return domain.WorkItem{}, err
	}
	projects, err := p.Projects(ctx)
	if err != nil {
		return domain.WorkItem{}, err
	}
	for _, proj := range projects {
		if proj.Identifier != ident {
			continue
		}
		it, err := scan(p.pool.QueryRow(ctx, `select `+cols+` from bt_items where project_id = $1 and seq = $2`, proj.ID, seq), proj)
		if errors.Is(err, pgx.ErrNoRows) {
			return it, domain.UserError("ไม่พบงาน " + key)
		}
		return it, err
	}
	return domain.WorkItem{}, domain.UserError("ไม่พบโปรเจกต์ " + ident)
}

func strOr(s *string, def string) string {
	if s == nil {
		return def
	}
	return *s
}

func arr(a *[]string) any {
	if a == nil {
		return nil
	}
	if *a == nil {
		return []string{}
	}
	return *a
}

func (p *Provider) CreateWorkItem(ctx context.Context, proj domain.Project, b domain.WorkItemWrite) (domain.WorkItem, error) {
	assignees := arr(b.Assignees)
	if assignees == nil {
		assignees = []string{}
	}
	return scan(p.pool.QueryRow(ctx, `insert into bt_items (id, project_id, seq, name, description_html, state, assignees, priority, start_date, target_date, parent_id)
    values ($1, $2, (select coalesce(max(seq),0)+1 from bt_items where project_id = $2), $3, $4, $5, $6, $7, $8::date, $9::date, $10)
    returning `+cols,
		uuid(), proj.ID, strOr(b.Name, "งานใหม่"), b.DescriptionHTML, strOr(b.State, "todo"), assignees, strOr(b.Priority, "none"), b.StartDate, b.TargetDate, b.Parent), proj)
}

func (p *Provider) UpdateWorkItem(ctx context.Context, proj domain.Project, id string, b domain.WorkItemWrite) (domain.WorkItem, error) {
	done := b.State != nil && *b.State == "done"
	it, err := scan(p.pool.QueryRow(ctx, `update bt_items set
        name = coalesce($3, name),
        state = coalesce($4, state),
        assignees = coalesce($5::text[], assignees),
        priority = coalesce($6, priority),
        start_date = coalesce($7::date, start_date),
        target_date = coalesce($8::date, target_date),
        completed_at = case when $4::text is null then completed_at when $9 then now() else null end,
        updated_at = now()
      where id = $1 and project_id = $2
      returning `+cols, id, proj.ID, b.Name, b.State, arr(b.Assignees), b.Priority, b.StartDate, b.TargetDate, done), proj)
	if errors.Is(err, pgx.ErrNoRows) {
		return it, domain.UserError("ไม่พบงาน")
	}
	return it, err
}

// AddToCycle does nothing: the built-in list has no cycles.
func (p *Provider) AddToCycle(context.Context, string, string, []string) error { return nil }

func (p *Provider) AddComment(ctx context.Context, _ domain.Project, itemID, html string) error {
	if _, err := p.pool.Exec(ctx, `insert into bt_comments (item_id, html) values ($1, $2)`, itemID, html); err != nil {
		return err
	}
	_, err := p.pool.Exec(ctx, `update bt_items set updated_at = now() where id = $1`, itemID)
	return err
}

func (p *Provider) ItemURL(it domain.WorkItem) string { return p.KeyURL(it.Key) }
func (p *Provider) KeyURL(key string) string {
	return p.publicURL + "/dashboard#" + url.PathEscape(key)
}
func (p *Provider) ProjectURL(domain.Project) string { return p.publicURL + "/dashboard" }
