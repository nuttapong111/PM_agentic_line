// Package plane is a thin client for the Plane REST API (v1), self-hosted or cloud.
// Docs: https://developers.plane.so/api-reference/introduction
package plane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
)

type Client struct {
	baseURL, apiKey, workspace string
	base                       string
	follow                     []string
	http                       *http.Client
	sleep                      func(time.Duration)

	mu        sync.Mutex
	itemsPath string // "work-items" on recent Plane, "issues" on older self-hosted builds
	cache     map[string]cached
}

type cached struct {
	at    time.Time
	value any
}

// New builds a client. follow lists project identifiers to follow; empty = all.
func New(baseURL, apiKey, workspace string, follow []string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	return &Client{
		baseURL: baseURL, apiKey: apiKey, workspace: workspace, follow: follow, http: hc,
		base: baseURL + "/api/v1/workspaces/" + workspace, cache: map[string]cached{}, sleep: time.Sleep,
	}
}

func (c *Client) Kind() string { return "plane" }

// WorkspaceKey identifies where data comes from, so stored history stays per workspace.
func (c *Client) WorkspaceKey() string {
	host := strings.TrimPrefix(strings.TrimPrefix(c.baseURL, "https://"), "http://")
	return "plane:" + host + "/" + c.workspace
}

// ---------------------------------------------------------------- low level

func (c *Client) request(ctx context.Context, method, u string, body any, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		var rd io.Reader
		if payload != nil {
			rd = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, rd)
		if err != nil {
			return err
		}
		req.Header.Set("X-API-Key", c.apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		res, err := c.http.Do(req)
		if err != nil {
			return err
		}
		data, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			// Plane allows 60 requests/minute. Wait until the reset time (or 5s) and retry.
			wait := 5 * time.Second
			if reset, _ := strconv.ParseInt(res.Header.Get("X-RateLimit-Reset"), 10, 64); reset > 0 {
				wait = max(time.Second, time.Until(time.Unix(reset, 0)))
			}
			c.sleep(min(wait, 30*time.Second))
			continue
		}
		if res.StatusCode >= 300 {
			return &domain.StatusError{Status: res.StatusCode, Msg: fmt.Sprintf("Plane %s %s → %d %s", method, strings.TrimPrefix(u, c.baseURL), res.StatusCode, domain.Truncate(string(data), 300))}
		}
		if out == nil || res.StatusCode == http.StatusNoContent || len(data) == 0 {
			return nil
		}
		return json.Unmarshal(data, out)
	}
}

// listAll follows cursor pagination. Endpoints that return a plain (maybe nested) array work too.
func (c *Client) listAll(ctx context.Context, path string, params map[string]string) ([]json.RawMessage, error) {
	var out []json.RawMessage
	cursor := ""
	for i := 0; i < 50; i++ {
		q := url.Values{"per_page": {"100"}}
		for k, v := range params {
			q.Set(k, v)
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var raw json.RawMessage
		if err := c.request(ctx, http.MethodGet, c.base+path+"?"+q.Encode(), nil, &raw); err != nil {
			return nil, err
		}
		if t := bytes.TrimSpace(raw); len(t) > 0 && t[0] == '[' {
			return append(out, flatten(t)...), nil
		}
		var page struct {
			Results         []json.RawMessage `json:"results"`
			NextCursor      *string           `json:"next_cursor"`
			NextPageResults bool              `json:"next_page_results"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Results...)
		if !page.NextPageResults || page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = *page.NextCursor
	}
	return out, nil
}

func flatten(arr []byte) []json.RawMessage {
	var xs []json.RawMessage
	_ = json.Unmarshal(arr, &xs)
	var out []json.RawMessage
	for _, x := range xs {
		if t := bytes.TrimSpace(x); len(t) > 0 && t[0] == '[' {
			var inner []json.RawMessage
			_ = json.Unmarshal(t, &inner)
			out = append(out, inner...)
			continue
		}
		out = append(out, x)
	}
	return out
}

func decodeAll[T any](raw []json.RawMessage) []T {
	out := make([]T, 0, len(raw))
	for _, r := range raw {
		var v T
		if json.Unmarshal(r, &v) == nil {
			out = append(out, v)
		}
	}
	return out
}

func cachedCall[T any](c *Client, key string, ttl time.Duration, load func() (T, error)) (T, error) {
	c.mu.Lock()
	if hit, ok := c.cache[key]; ok && time.Since(hit.at) < ttl {
		c.mu.Unlock()
		return hit.value.(T), nil
	}
	c.mu.Unlock()
	v, err := load()
	if err != nil {
		return v, err
	}
	c.mu.Lock()
	c.cache[key] = cached{time.Now(), v}
	c.mu.Unlock()
	return v, nil
}

func (c *Client) invalidate(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.cache {
		if strings.HasPrefix(k, prefix) {
			delete(c.cache, k)
		}
	}
}

func (c *Client) items(ctx context.Context) (string, error) {
	c.mu.Lock()
	p := c.itemsPath
	c.mu.Unlock()
	if p != "" {
		return p, nil
	}
	projects, err := c.Projects(ctx)
	if err != nil {
		return "", err
	}
	p = "work-items"
	if len(projects) > 0 {
		err := c.request(ctx, http.MethodGet, c.base+"/projects/"+projects[0].ID+"/work-items/?per_page=1", nil, nil)
		if domain.HTTPStatus(err) == http.StatusNotFound {
			p = "issues"
		} else if err != nil {
			return "", err
		}
	}
	c.mu.Lock()
	c.itemsPath = p
	c.mu.Unlock()
	return p, nil
}

// ---------------------------------------------------------------- reads

func (c *Client) Me(ctx context.Context) (domain.Member, error) {
	return cachedCall(c, "me", time.Hour, func() (domain.Member, error) {
		var m domain.Member
		err := c.request(ctx, http.MethodGet, c.baseURL+"/api/v1/users/me/", nil, &m)
		return m, err
	})
}

func (c *Client) AllProjects(ctx context.Context) ([]domain.Project, error) {
	return cachedCall(c, "projects", 10*time.Minute, func() ([]domain.Project, error) {
		raw, err := c.listAll(ctx, "/projects/", nil)
		return decodeAll[domain.Project](raw), err
	})
}

func (c *Client) Projects(ctx context.Context) ([]domain.Project, error) {
	all, err := c.AllProjects(ctx)
	if err != nil || len(c.follow) == 0 {
		return all, err
	}
	want := map[string]bool{}
	for _, f := range c.follow {
		want[strings.ToUpper(f)] = true
	}
	out := []domain.Project{}
	for _, p := range all {
		if want[strings.ToUpper(p.Identifier)] {
			out = append(out, p)
		}
	}
	return out, nil
}

func (c *Client) States(ctx context.Context, projectID string) ([]domain.State, error) {
	return cachedCall(c, "states:"+projectID, 10*time.Minute, func() ([]domain.State, error) {
		raw, err := c.listAll(ctx, "/projects/"+projectID+"/states/", nil)
		return decodeAll[domain.State](raw), err
	})
}

func (c *Client) Members(ctx context.Context, projectID string) ([]domain.Member, error) {
	return cachedCall(c, "members:"+projectID, 10*time.Minute, func() ([]domain.Member, error) {
		raw, err := c.listAll(ctx, "/projects/"+projectID+"/project-members/", nil)
		if err != nil {
			return nil, err
		}
		return decodeMembers(raw), nil
	})
}

// decodeMembers reads member rows; some versions wrap the user in `member`.
func decodeMembers(raw []json.RawMessage) []domain.Member {
	out := make([]domain.Member, 0, len(raw))
	for _, r := range raw {
		// Some versions wrap the user in `member`.
		var w struct {
			Member json.RawMessage `json:"member"`
		}
		_ = json.Unmarshal(r, &w)
		src := r
		if t := bytes.TrimSpace(w.Member); len(t) > 0 && t[0] == '{' {
			src = t
		}
		var m domain.Member
		if json.Unmarshal(src, &m) == nil && m.ID != "" {
			out = append(out, m)
		}
	}
	return out
}

func (c *Client) Labels(ctx context.Context, projectID string) ([]domain.Label, error) {
	return cachedCall(c, "labels:"+projectID, 10*time.Minute, func() ([]domain.Label, error) {
		raw, err := c.listAll(ctx, "/projects/"+projectID+"/labels/", nil)
		return decodeAll[domain.Label](raw), err
	})
}

func (c *Client) Cycles(ctx context.Context, projectID string) ([]domain.Cycle, error) {
	return cachedCall(c, "cycles:"+projectID, 5*time.Minute, func() ([]domain.Cycle, error) {
		raw, err := c.listAll(ctx, "/projects/"+projectID+"/cycles/", map[string]string{"cycle_view": "all"})
		return decodeAll[domain.Cycle](raw), err
	})
}

func (c *Client) Modules(ctx context.Context, projectID string) ([]domain.Module, error) {
	return cachedCall(c, "modules:"+projectID, 5*time.Minute, func() ([]domain.Module, error) {
		raw, err := c.listAll(ctx, "/projects/"+projectID+"/modules/", nil)
		return decodeAll[domain.Module](raw), err
	})
}

type link struct {
	Issue     json.RawMessage `json:"issue"`
	ID        string          `json:"id"`
	CreatedAt string          `json:"created_at"`
}

func (c *Client) CycleLinks(ctx context.Context, projectID, cycleID string) ([]domain.CycleLink, error) {
	return cachedCall(c, "cycleitems:"+projectID+":"+cycleID, 2*time.Minute, func() ([]domain.CycleLink, error) {
		raw, err := c.listAll(ctx, "/projects/"+projectID+"/cycles/"+cycleID+"/cycle-issues/", nil)
		out := []domain.CycleLink{}
		for _, l := range decodeAll[link](raw) {
			id := idOf(l.Issue)
			if id == "" {
				id = l.ID
			}
			out = append(out, domain.CycleLink{IssueID: id, AddedAt: l.CreatedAt})
		}
		return out, err
	})
}

func (c *Client) ModuleItemIDs(ctx context.Context, projectID, moduleID string) ([]string, error) {
	return cachedCall(c, "moduleitems:"+projectID+":"+moduleID, 2*time.Minute, func() ([]string, error) {
		raw, err := c.listAll(ctx, "/projects/"+projectID+"/modules/"+moduleID+"/module-issues/", nil)
		out := []string{}
		for _, l := range decodeAll[link](raw) {
			id := idOf(l.Issue)
			if id == "" {
				id = l.ID
			}
			out = append(out, id)
		}
		return out, err
	})
}

func (c *Client) WorkItems(ctx context.Context, p domain.Project) ([]domain.WorkItem, error) {
	return cachedCall(c, "items:"+p.ID, 2*time.Minute, func() ([]domain.WorkItem, error) {
		path, err := c.items(ctx)
		if err != nil {
			return nil, err
		}
		raw, err := c.listAll(ctx, "/projects/"+p.ID+"/"+path+"/", nil)
		if err != nil {
			return nil, err
		}
		out := make([]domain.WorkItem, 0, len(raw))
		for _, r := range decodeAll[rawItem](raw) {
			out = append(out, r.normalise(p))
		}
		return out, nil
	})
}

func (c *Client) WorkItemByKey(ctx context.Context, key string) (domain.WorkItem, error) {
	ident, seq, err := domain.SplitKey(key)
	if err != nil {
		return domain.WorkItem{}, err
	}
	projects, err := c.Projects(ctx)
	if err != nil {
		return domain.WorkItem{}, err
	}
	var project *domain.Project
	for i := range projects {
		if strings.ToUpper(projects[i].Identifier) == ident {
			project = &projects[i]
		}
	}
	if project == nil {
		return domain.WorkItem{}, domain.UserError("ไม่พบโปรเจกต์ที่มีรหัส " + ident)
	}
	path, err := c.items(ctx)
	if err != nil {
		return domain.WorkItem{}, err
	}
	var r rawItem
	if err := c.request(ctx, http.MethodGet, fmt.Sprintf("%s/%s/%s-%d/", c.base, path, ident, seq), nil, &r); err != nil {
		return domain.WorkItem{}, err
	}
	return r.normalise(*project), nil
}

// ---------------------------------------------------------------- writes

func (c *Client) CreateWorkItem(ctx context.Context, p domain.Project, body domain.WorkItemWrite) (domain.WorkItem, error) {
	path, err := c.items(ctx)
	if err != nil {
		return domain.WorkItem{}, err
	}
	var r rawItem
	err = c.request(ctx, http.MethodPost, c.base+"/projects/"+p.ID+"/"+path+"/", body, &r)
	c.invalidate("items:" + p.ID)
	return r.normalise(p), err
}

func (c *Client) UpdateWorkItem(ctx context.Context, p domain.Project, id string, body domain.WorkItemWrite) (domain.WorkItem, error) {
	path, err := c.items(ctx)
	if err != nil {
		return domain.WorkItem{}, err
	}
	var r rawItem
	err = c.request(ctx, http.MethodPatch, c.base+"/projects/"+p.ID+"/"+path+"/"+id+"/", body, &r)
	c.invalidate("items:" + p.ID)
	return r.normalise(p), err
}

func (c *Client) AddToCycle(ctx context.Context, projectID, cycleID string, itemIDs []string) error {
	err := c.request(ctx, http.MethodPost, c.base+"/projects/"+projectID+"/cycles/"+cycleID+"/cycle-issues/", map[string]any{"issues": itemIDs}, nil)
	c.invalidate("cycleitems:" + projectID + ":" + cycleID)
	return err
}

// NewProject creates a project; Plane makes the token's user its admin.
func (c *Client) NewProject(ctx context.Context, in domain.ProjectWrite) (domain.Project, error) {
	body := map[string]any{"name": in.Name, "identifier": in.Identifier}
	if in.Description != "" {
		body["description"] = in.Description
	}
	var out domain.Project
	err := c.request(ctx, http.MethodPost, c.base+"/projects/", body, &out)
	c.invalidate("projects")
	return out, err
}

// WorkspaceMembers lists everyone in the workspace.
func (c *Client) WorkspaceMembers(ctx context.Context) ([]domain.Member, error) {
	return cachedCall(c, "wsmembers", 10*time.Minute, func() ([]domain.Member, error) {
		raw, err := c.listAll(ctx, "/members/", nil)
		if err != nil {
			return nil, err
		}
		return decodeMembers(raw), nil
	})
}

// AddProjectMember adds a workspace member to a project. Not every Plane version allows this
// through the API; callers fall back to MembersURL.
func (c *Client) AddProjectMember(ctx context.Context, projectID, memberID string, role int) error {
	err := c.request(ctx, http.MethodPost, c.base+"/projects/"+projectID+"/project-members/", map[string]any{"member": memberID, "role": role}, nil)
	c.invalidate("members:" + projectID)
	return err
}

func (c *Client) MembersURL(p domain.Project) string {
	return c.baseURL + "/" + c.workspace + "/projects/" + p.ID + "/settings/members"
}

// CreateCycle creates a cycle owned by the token's user.
func (c *Client) CreateCycle(ctx context.Context, projectID string, in domain.CycleWrite) (domain.Cycle, error) {
	me, err := c.Me(ctx)
	if err != nil {
		return domain.Cycle{}, err
	}
	body := map[string]any{"name": in.Name, "start_date": in.StartDate, "end_date": in.EndDate, "project_id": projectID, "owned_by": me.ID}
	if in.Description != "" {
		body["description"] = in.Description
	}
	var out domain.Cycle
	err = c.request(ctx, http.MethodPost, c.base+"/projects/"+projectID+"/cycles/", body, &out)
	c.invalidate("cycles:" + projectID)
	return out, err
}

// AddToModule puts work items into a module (Plane keeps the item's other modules).
func (c *Client) AddToModule(ctx context.Context, projectID, moduleID string, itemIDs []string) error {
	err := c.request(ctx, http.MethodPost, c.base+"/projects/"+projectID+"/modules/"+moduleID+"/module-issues/", map[string]any{"issues": itemIDs}, nil)
	c.invalidate("moduleitems:" + projectID + ":" + moduleID)
	return err
}

func (c *Client) AddComment(ctx context.Context, p domain.Project, itemID, html string) error {
	path, err := c.items(ctx)
	if err != nil {
		return err
	}
	return c.request(ctx, http.MethodPost, c.base+"/projects/"+p.ID+"/"+path+"/"+itemID+"/comments/", map[string]any{"comment_html": html}, nil)
}

func (c *Client) KeyURL(key string) string {
	return c.baseURL + "/" + c.workspace + "/browse/" + key + "/"
}

func (c *Client) ItemURL(it domain.WorkItem) string { return c.KeyURL(it.Key) }

func (c *Client) ProjectURL(p domain.Project) string {
	return c.baseURL + "/" + c.workspace + "/projects/" + p.ID + "/issues"
}

// ---------------------------------------------------------------- normalising

type rawItem struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	SequenceID  int               `json:"sequence_id"`
	Priority    *string           `json:"priority"`
	State       json.RawMessage   `json:"state"`
	StateID     string            `json:"state_id"`
	Assignees   []json.RawMessage `json:"assignees"`
	AssigneeIDs []string          `json:"assignee_ids"`
	Labels      []json.RawMessage `json:"labels"`
	LabelIDs    []string          `json:"label_ids"`
	StartDate   *string           `json:"start_date"`
	TargetDate  *string           `json:"target_date"`
	CompletedAt *string           `json:"completed_at"`
	CreatedAt   string            `json:"created_at"`
	UpdatedAt   *string           `json:"updated_at"`
	Parent      json.RawMessage   `json:"parent"`
	ParentID    *string           `json:"parent_id"`
}

// idOf reads an id given as a string or as an object with an id.
func idOf(v json.RawMessage) string {
	if len(v) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	var o struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(v, &o)
	return o.ID
}

func ids(xs []json.RawMessage, extra []string) []string {
	out := []string{}
	for _, x := range xs {
		if id := idOf(x); id != "" {
			out = append(out, id)
		}
	}
	return append(out, extra...)
}

func (r rawItem) normalise(p domain.Project) domain.WorkItem {
	prio := "none"
	if r.Priority != nil {
		prio = *r.Priority
	}
	state := idOf(r.State)
	if state == "" {
		state = r.StateID
	}
	updated := r.CreatedAt
	if r.UpdatedAt != nil {
		updated = *r.UpdatedAt
	}
	parent := r.ParentID
	if id := idOf(r.Parent); id != "" {
		parent = &id
	}
	return domain.WorkItem{
		ID: r.ID, ProjectID: p.ID, Key: fmt.Sprintf("%s-%d", p.Identifier, r.SequenceID), Name: r.Name, Priority: prio,
		StateID: state, AssigneeIDs: ids(r.Assignees, r.AssigneeIDs), LabelIDs: ids(r.Labels, r.LabelIDs),
		StartDate: r.StartDate, TargetDate: r.TargetDate, CompletedAt: r.CompletedAt, CreatedAt: r.CreatedAt,
		UpdatedAt: updated, ParentID: parent,
	}
}
