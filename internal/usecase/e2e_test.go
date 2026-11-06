package usecase

// End-to-end tests: the real adapters against a mocked Plane API (httptest), the real LINE
// presenter, a scripted Claude, and (when TEST_DATABASE_URL is set) a real PostgreSQL.
// Ported from the old scripts/selftest.ts and scripts/selftest-features.ts.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/adapter/builtin"
	"github.com/nuttapong111/pm_agentic_line/internal/adapter/crypto"
	"github.com/nuttapong111/pm_agentic_line/internal/adapter/filestore"
	"github.com/nuttapong111/pm_agentic_line/internal/adapter/line"
	"github.com/nuttapong111/pm_agentic_line/internal/adapter/plane"
	"github.com/nuttapong111/pm_agentic_line/internal/adapter/postgres"
	"github.com/nuttapong111/pm_agentic_line/internal/config"
	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

var bkk = mustLoc("Asia/Bangkok")

func mustLoc(n string) *time.Location {
	l, err := time.LoadLocation(n)
	if err != nil {
		panic(err)
	}
	return l
}

const day = 24 * time.Hour

func dd(n int) string { return domain.DateIn(time.Now().Add(time.Duration(n)*day), bkk) }
func iso(n int) string {
	return time.Now().Add(time.Duration(n) * day).UTC().Format("2006-01-02T15:04:05.000Z")
}
func pageOf(results ...any) map[string]any {
	if results == nil {
		results = []any{}
	}
	return map[string]any{"results": results, "next_cursor": nil, "next_page_results": false}
}

type jo = map[string]any

// mockPlane serves the given paths (relative to the workspace, or /users/me/) and records comments.
type mockPlane struct {
	*httptest.Server
	mu       sync.Mutex
	data     map[string]any
	comments []string
	posts    []string
}

func newMockPlane(t *testing.T, data map[string]any) *mockPlane {
	m := &mockPlane{data: data}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") == "bad" {
			w.WriteHeader(401)
			io.WriteString(w, "{}")
			return
		}
		key := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api/v1/workspaces/ws"), "/api/v1")
		if r.Method == http.MethodPost || r.Method == http.MethodPatch {
			m.mu.Lock()
			m.posts = append(m.posts, r.Method+" "+key)
			if strings.HasSuffix(key, "/comments/") {
				m.comments = append(m.comments, key)
			}
			m.mu.Unlock()
			w.WriteHeader(201)
			io.WriteString(w, "{}")
			return
		}
		body, ok := m.data[key]
		if !ok {
			w.WriteHeader(404)
			io.WriteString(w, "not found "+key)
			return
		}
		if strings.HasSuffix(key, "work-items/") && r.URL.Query().Get("per_page") == "1" {
			body = pageOf()
		}
		json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(m.Close)
	return m
}

var states = pageOf(
	jo{"id": "s-todo", "name": "Todo", "group": "unstarted"},
	jo{"id": "s-prog", "name": "In Progress", "group": "started"},
	jo{"id": "s-block", "name": "Blocked", "group": "started"},
	jo{"id": "s-done", "name": "Done", "group": "completed"},
)

// scriptedLLM returns the queued responses in order and records the requests.
type scriptedLLM struct {
	responses []port.LLMResponse
	requests  []port.LLMRequest
}

func (s *scriptedLLM) Create(_ context.Context, _ string, req port.LLMRequest) (port.LLMResponse, error) {
	s.requests = append(s.requests, req)
	if len(s.responses) == 0 {
		return port.LLMResponse{Content: []domain.Block{domain.TextBlock("ok")}, StopReason: "end_turn"}, nil
	}
	r := s.responses[0]
	s.responses = s.responses[1:]
	return r, nil
}
func (s *scriptedLLM) CheckModel(context.Context, string, string) error { return nil }

type env struct {
	app   *App
	plane *mockPlane
	llm   *scriptedLLM
	db    *postgres.DB
}

func newEnv(t *testing.T, data map[string]any, withDB bool) *env {
	t.Helper()
	pm := newMockPlane(t, data)
	vars := map[string]string{
		"LINE_CHANNEL_SECRET": "x", "LINE_CHANNEL_ACCESS_TOKEN": "x", "DASHBOARD_TOKEN": "t",
		"PLANE_BASE_URL": pm.URL, "PLANE_API_KEY": "envtoken1234", "PLANE_WORKSPACE_SLUG": "ws",
		"ANTHROPIC_API_KEY": "sk-test", "OWNER_LINE_USER_ID": "U1",
	}
	cfg, err := config.LoadFrom(func(k string) string { return vars[k] })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	defaults := domain.PrefDefaults{Nicknames: cfg.Nicknames, DigestCron: cfg.DigestCron, StaleDays: cfg.StaleDays, BugLabel: cfg.BugLabel}
	secrets := crypto.New(cfg.EncryptionKey)
	e := &env{plane: pm, llm: &scriptedLLM{}}
	var (
		store   *Store
		records port.RecordRepo  = NoRecords{}
		history port.HistoryRepo = NoHistory{}
		btTasks port.TaskProvider
		btAdmin port.BuiltinAdmin
	)
	if withDB {
		url := os.Getenv("TEST_DATABASE_URL")
		if url == "" {
			t.Skip("set TEST_DATABASE_URL to run the PostgreSQL tests")
		}
		pre, err := postgres.Open(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pre.Pool.Exec(ctx, `drop table if exists pm_records, app_state, activity_log, usage_log, metrics_daily, bt_comments, bt_items, bt_projects cascade`); err != nil {
			t.Fatal(err)
		}
		pre.Close()
		if e.db, err = postgres.Open(ctx, url); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(e.db.Close)
		state := e.db.State()
		t.Cleanup(state.Flush)
		if store, err = NewStore(ctx, state, nil, defaults, time.Now); err != nil {
			t.Fatal(err)
		}
		records, history = e.db.Records(), e.db.History()
		bt := builtin.New(e.db.Pool, cfg.PublicURL, store.Nicknames)
		if err := bt.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		btTasks, btAdmin = bt, bt
	} else {
		if store, err = NewStore(ctx, filestore.New(t.TempDir()), nil, defaults, time.Now); err != nil {
			t.Fatal(err)
		}
	}
	tasks := NewTasks(cfg, store, secrets, history, func(base, ws, token string, follow []string) port.TaskProvider {
		return plane.New(base, token, ws, follow, pm.Client())
	}, btTasks)
	e.app = New(Deps{Cfg: cfg, Loc: bkk, Now: time.Now, Store: store, Tasks: tasks, Builtin: btAdmin,
		Records: records, History: history, Secrets: secrets, LLM: e.llm, View: line.Presenter{}})
	return e
}

type checker struct{ t *testing.T }

func (c checker) check(name string, ok bool, got ...any) {
	c.t.Helper()
	if !ok {
		b, _ := json.Marshal(got)
		s := string(b)
		if len(s) > 600 {
			s = s[:600]
		}
		c.t.Errorf("FAIL %s (got %s)", name, s)
	}
}

func keys(vs []domain.ItemView) string {
	ks := make([]string, len(vs))
	for i, v := range vs {
		ks[i] = v.Key
	}
	return strings.Join(ks, ",")
}

// ---------------------------------------------------------------- selftest.ts

func metricsData() map[string]any {
	return map[string]any{
		"/users/me/":                    jo{"id": "u1", "display_name": "ton", "email": "ton@corp.test"},
		"/projects/":                    pageOf(jo{"id": "p1", "name": "FDA Big Data", "identifier": "FDA"}),
		"/projects/p1/states/":          states,
		"/projects/p1/project-members/": []any{jo{"member": jo{"id": "u1", "display_name": "ton"}}, jo{"id": "u2", "display_name": "may"}},
		"/projects/p1/labels/":          pageOf(jo{"id": "l-bug", "name": "Bug"}),
		"/projects/p1/cycles/":          pageOf(jo{"id": "c1", "name": "Sprint 1", "start_date": dd(-7), "end_date": dd(7)}),
		"/projects/p1/modules/":         pageOf(jo{"id": "m1", "name": "Login", "start_date": dd(-10), "target_date": dd(2)}),
		"/projects/p1/cycles/c1/cycle-issues/": pageOf(
			jo{"id": "x1", "issue": "i1", "created_at": iso(-7)}, jo{"id": "x2", "issue": "i2", "created_at": iso(-7)},
			jo{"id": "x3", "issue": "i3", "created_at": iso(-2)}, jo{"id": "x4", "issue": "i4", "created_at": iso(-7)},
		),
		"/projects/p1/modules/m1/module-issues/": pageOf(jo{"id": "y1", "issue": "i1"}, jo{"id": "y2", "issue": "i2"}),
		"/projects/p1/work-items/": pageOf(
			jo{"id": "i1", "name": "API login", "sequence_id": 1, "state": "s-prog", "assignees": []string{"u1"}, "target_date": dd(-3), "created_at": iso(-10), "updated_at": iso(-8), "priority": "high"},
			jo{"id": "i2", "name": "OTP", "sequence_id": 2, "state": "s-block", "assignees": []string{"u2"}, "target_date": dd(0), "created_at": iso(-10), "updated_at": iso(-1), "labels": []string{"l-bug"}, "priority": "urgent"},
			jo{"id": "i3", "name": "UI", "sequence_id": 3, "state": "s-todo", "assignees": []string{}, "target_date": dd(3), "created_at": iso(-2)},
			jo{"id": "i4", "name": "Docs", "sequence_id": 4, "state": "s-done", "completed_at": iso(0), "created_at": iso(-10)},
		),
	}
}

func TestMetricsAndCards(t *testing.T) {
	e := newEnv(t, metricsData(), false)
	c := checker{t}
	ctx := context.Background()
	ps, err := e.app.tasks().Projects(ctx)
	if err != nil || len(ps) != 1 {
		t.Fatalf("projects: %v %v", ps, err)
	}
	m, err := e.app.ProjectMetrics(ctx, ps[0])
	if err != nil {
		t.Fatal(err)
	}
	c.check("open count", m.Counts.Open == 3, m.Counts)
	c.check("overdue = FDA-1", keys(m.Overdue) == "FDA-1", m.Overdue)
	c.check("due today = FDA-2", keys(m.DueToday) == "FDA-2", m.DueToday)
	c.check("blocked = FDA-2", keys(m.Blocked) == "FDA-2", m.Blocked)
	c.check("stale = FDA-1", keys(m.Stale) == "FDA-1", m.Stale)
	c.check("unassigned = 1", m.Counts.Unassigned == 1, m.Counts)
	c.check("done this week = 1", m.Counts.DoneThisWeek == 1, m.Counts)
	c.check("urgent bugs = 1", m.Counts.UrgentBugs == 1, m.Counts)
	c.check("cycle total 4, done 1", m.Cycle != nil && m.Cycle.Total == 4 && m.Cycle.Done == 1, m.Cycle)
	c.check("cycle added midway = 1", m.Cycle != nil && m.Cycle.AddedMidway == 1, m.Cycle)
	c.check("module listed", len(m.Modules) == 1, m.Modules)
	hasTon := false
	for _, w := range m.Workload {
		hasTon = hasTon || strings.Contains(w.Name, "ton")
	}
	c.check("member wrapped in `member` is resolved", hasTon, m.Workload)

	v := line.Presenter{}
	for _, card := range []port.Message{v.Status(m, "https://x/dashboard"), v.Digest([]domain.ProjectMetrics{m}, "วันจันทร์ 1 พ.ย.", "https://x/dashboard", nil)} {
		b, _ := json.Marshal(card)
		c.check("card serialises < 30KB", len(b) < 30_000 && card["type"] == "flex", len(b))
	}
	// empty lists marshal as [] for the dashboard
	b, _ := json.Marshal(m)
	c.check("metrics JSON has no null lists", !strings.Contains(string(b), `"dueNext7":null`) && !strings.Contains(string(b), `"modules":null`), string(b))
}

// A whole chat turn: Claude calls create_work_items, the owner confirms, the item is created in Plane.
func TestAgentConfirmFlow(t *testing.T) {
	e := newEnv(t, metricsData(), false)
	c := checker{t}
	ctx := context.Background()
	e.llm.responses = []port.LLMResponse{
		{StopReason: "tool_use", Content: []domain.Block{
			domain.TextBlock("สร้างให้ครับ"),
			{"type": "tool_use", "id": "tu1", "name": "create_work_items", "input": jo{"items": []any{jo{"name": "หน้า login", "target_date": dd(5)}}}},
		}},
		{StopReason: "end_turn", Content: []domain.Block{domain.TextBlock("เรียบร้อยครับ")}},
	}
	msgs := e.app.HandleText(ctx, "สร้างงานหน้า login ส่งอีก 5 วัน")
	snap := e.app.Store.Snapshot()
	c.check("one pending card", snap.Pending != nil && len(snap.Pending.Writes) == 1, snap.Pending)
	c.check("reply = text + confirm card", len(msgs) == 2 && msgs[1]["type"] == "flex", msgs)
	c.check("system prompt cached + dynamic context", len(e.llm.requests) == 1 && e.llm.requests[0].System[0]["cache_control"] != nil &&
		strings.Contains(e.llm.requests[0].System[1]["text"].(string), "FDA Big Data") == false, e.llm.requests[0].System)
	if snap.Pending == nil {
		return
	}
	out := e.app.HandleMenu(ctx, "confirm:"+snap.Pending.ID)
	c.check("result card after confirm", len(out) == 1 && out[0]["type"] == "flex", out)
	c.check("pending cleared", e.app.Store.Snapshot().Pending == nil)
	hist := e.app.Store.Snapshot().Conversation.Messages
	last := hist[len(hist)-1]
	c.check("tool_result appended for Claude", last.Role == "user" && last.Content[0]["type"] == "tool_result" && last.Content[0]["tool_use_id"] == "tu1", last)
	c.check("expired card refused", strings.Contains(toJSON(e.app.HandleMenu(ctx, "confirm:nope")), "หมดอายุ"))
	c.check("reset command", strings.Contains(toJSON(e.app.HandleText(ctx, "reset")), "เริ่มบทสนทนาใหม่") && len(e.app.Store.Snapshot().Conversation.Messages) == 0)
}

func TestModuleAssignment(t *testing.T) {
	data := metricsData()
	data["/work-items/FDA-3/"] = jo{"id": "i3", "name": "UI", "sequence_id": 3, "state": "s-todo", "project": "p1", "assignees": []string{}}
	e := newEnv(t, data, false)
	c := checker{t}
	ctx := context.Background()
	const addPath = "POST /projects/p1/modules/m1/module-issues/"
	posted := func() bool {
		e.plane.mu.Lock()
		defer e.plane.mu.Unlock()
		for _, p := range e.plane.posts {
			if p == addPath {
				return true
			}
		}
		return false
	}

	pv, err := e.app.prepareUpdate(ctx, json.RawMessage(`{"key":"FDA-3","module":"login"}`))
	c.check("update preview resolves module", err == nil && pv.Plan.ModuleID != nil && *pv.Plan.ModuleID == "m1" && strings.Contains(toJSON(pv.Lines), "Login"), err, pv)
	if err == nil {
		_, err = e.app.ExecuteWrite(ctx, pv.Plan)
		c.check("update adds item to module", err == nil && posted(), err, e.plane.posts)
	}

	_, err = e.app.prepareUpdate(ctx, json.RawMessage(`{"key":"FDA-3","module":"Payment"}`))
	c.check("unknown module lists choices", err != nil && strings.Contains(err.Error(), "Login"), err)

	e.plane.mu.Lock()
	e.plane.posts = nil
	e.plane.mu.Unlock()
	pv, err = e.app.prepareCreate(ctx, json.RawMessage(`{"project":"FDA","module":"Login","items":[{"name":"หน้า reset password"}]}`))
	c.check("create preview shows module", err == nil && pv.Plan.ModuleID != nil && strings.Contains(pv.Subtitle, "Module Login"), err, pv)
	if err == nil {
		_, err = e.app.ExecuteWrite(ctx, pv.Plan)
		c.check("create adds items to module", err == nil && posted(), err, e.plane.posts)
	}
}

func TestCreateCycle(t *testing.T) {
	data := metricsData()
	data["/work-items/FDA-3/"] = jo{"id": "i3", "name": "UI", "sequence_id": 3, "state": "s-todo", "project": "p1", "assignees": []string{}}
	e := newEnv(t, data, false)
	c := checker{t}
	ctx := context.Background()
	c.check("create_cycle needs confirm", e.app.IsWriteTool("create_cycle"))

	pv, err := e.app.PrepareWrite(ctx, "create_cycle", json.RawMessage(`{"project":"FDA","name":"Sprint 2","start_date":"`+dd(8)+`","end_date":"`+dd(21)+`","item_keys":["FDA-3"]}`))
	c.check("preview has dates and item, no overlap", err == nil && pv.Plan.Cycle != nil && len(pv.Plan.ItemIDs) == 1 && len(pv.Warnings) == 0 && strings.Contains(toJSON(pv.Lines), "FDA-3"), err, pv)
	if err == nil {
		r, err := e.app.ExecuteWrite(ctx, pv.Plan)
		e.plane.mu.Lock()
		posts := strings.Join(e.plane.posts, "|")
		e.plane.mu.Unlock()
		c.check("cycle created then item added", err == nil && strings.Contains(posts, "POST /projects/p1/cycles/") && strings.Contains(r["summary"].(string), "Sprint 2"), err, posts, r)
	}

	pv, err = e.app.PrepareWrite(ctx, "create_cycle", json.RawMessage(`{"project":"FDA","name":"Sprint 3","start_date":"`+dd(0)+`","end_date":"`+dd(10)+`"}`))
	c.check("overlap warning", err == nil && len(pv.Warnings) == 1 && strings.Contains(pv.Warnings[0], "Sprint 1"), err, pv)
	_, err = e.app.PrepareWrite(ctx, "create_cycle", json.RawMessage(`{"project":"FDA","name":"sprint 1","start_date":"`+dd(30)+`","end_date":"`+dd(40)+`"}`))
	c.check("duplicate name refused", err != nil, err)
	_, err = e.app.PrepareWrite(ctx, "create_cycle", json.RawMessage(`{"project":"FDA","name":"X","start_date":"`+dd(10)+`","end_date":"`+dd(5)+`"}`))
	c.check("end before start refused", err != nil, err)
}

func TestNeedProjectPicker(t *testing.T) {
	data := metricsData()
	data["/projects/"] = pageOf(jo{"id": "p1", "name": "FDA Big Data", "identifier": "FDA"}, jo{"id": "p2", "name": "ERP", "identifier": "ERP"})
	e := newEnv(t, data, false)
	e.llm.responses = []port.LLMResponse{
		{StopReason: "tool_use", Content: []domain.Block{{"type": "tool_use", "id": "tu1", "name": "project_status", "input": jo{}}}},
		{StopReason: "end_turn", Content: []domain.Block{domain.TextBlock("หมายถึงโปรเจกต์ไหนครับ")}},
	}
	msgs := e.app.HandleText(context.Background(), "สถานะเป็นไง")
	b := toJSON(msgs)
	checker{t}.check("project picker shown", strings.Contains(b, "project:p2"), b)
}

// ---------------------------------------------------------------- selftest-features.ts

func featuresData() map[string]any {
	i1 := jo{"id": "i1", "name": "API login", "sequence_id": 1, "state": "s-prog", "assignees": []string{"u1"}, "target_date": dd(-3), "created_at": iso(-10), "updated_at": iso(-8), "priority": "high", "project": "p1"}
	return map[string]any{
		"/work-items/FDA-1/":                     i1,
		"/users/me/":                             jo{"id": "u1", "display_name": "ton", "email": "ton@corp.test"},
		"/projects/":                             pageOf(jo{"id": "p1", "name": "FDA Big Data", "identifier": "FDA"}),
		"/projects/p1/states/":                   states,
		"/projects/p1/project-members/":          []any{jo{"member": jo{"id": "u1", "display_name": "ton", "email": "ton@corp.test"}}, jo{"id": "u2", "display_name": "may"}},
		"/projects/p1/labels/":                   pageOf(),
		"/projects/p1/cycles/":                   pageOf(jo{"id": "c0", "name": "Sprint 0", "start_date": dd(-21), "end_date": dd(-8)}, jo{"id": "c1", "name": "Sprint 1", "start_date": dd(-7), "end_date": dd(7)}),
		"/projects/p1/modules/":                  pageOf(jo{"id": "m1", "name": "Login", "start_date": dd(-10), "target_date": dd(2)}),
		"/projects/p1/cycles/c1/cycle-issues/":   pageOf(jo{"id": "x1", "issue": "i1", "created_at": iso(-7)}, jo{"id": "x3", "issue": "i3", "created_at": iso(-2)}),
		"/projects/p1/cycles/c0/cycle-issues/":   pageOf(jo{"id": "x2", "issue": "i2", "created_at": iso(-21)}, jo{"id": "x4", "issue": "i4", "created_at": iso(-21)}),
		"/projects/p1/modules/m1/module-issues/": pageOf(jo{"id": "y1", "issue": "i1"}, jo{"id": "y2", "issue": "i2"}),
		"/projects/p1/work-items/": pageOf(
			jo{"id": "i1", "name": "API login", "sequence_id": 1, "state": "s-prog", "assignees": []string{"u1"}, "target_date": dd(-3), "created_at": iso(-10), "updated_at": iso(-8), "priority": "high"},
			jo{"id": "i2", "name": "OTP", "sequence_id": 2, "state": "s-block", "assignees": []string{"u2"}, "target_date": dd(-9), "created_at": iso(-20), "updated_at": iso(-9)},
			jo{"id": "i3", "name": "UI", "sequence_id": 3, "state": "s-todo", "assignees": []string{"u1"}, "target_date": dd(9), "created_at": iso(-2)},
			jo{"id": "i4", "name": "Docs", "sequence_id": 4, "state": "s-done", "completed_at": iso(-1), "created_at": iso(-10)},
		),
	}
}

func raw(v any) json.RawMessage { return json.RawMessage(toJSON(v)) }

func TestFeaturesWithPostgres(t *testing.T) {
	e := newEnv(t, featuresData(), true)
	a, c, ctx := e.app, checker{t}, context.Background()
	write := func(name string, in any) (domain.Preview, Result) {
		t.Helper()
		pv, err := a.PrepareWrite(ctx, name, raw(in))
		if err != nil {
			t.Fatalf("%s prepare: %v", name, err)
		}
		r, err := a.ExecuteWrite(ctx, pv.Plan)
		if err != nil {
			t.Fatalf("%s execute: %v", name, err)
		}
		return pv, r
	}

	c.check("env provider is plane", a.tasks().Kind() == port.KindPlane)
	wsKey := "plane:" + strings.TrimPrefix(e.plane.URL, "http://") + "/ws"
	c.check("workspace key", a.ws() == wsKey, a.ws())
	a.Store.Update(func(s *domain.AppState) { s.CurrentProjectID = ptr("p1") })

	// risk log
	pv, r := write("log_risk", jo{"type": "risk", "title": "API หน่วยงานล่าช้า", "impact": "high", "likelihood": "medium", "mitigation": "ใช้ CSV"})
	hasHigh := false
	for _, l := range pv.Lines {
		hasHigh = hasHigh || strings.Contains(l.Sub, "สูง")
	}
	c.check("risk preview has impact line", hasHigh, pv.Lines)
	c.check("risk saved R-01", r["record"] == "R-01", r)
	_, r = write("log_risk", jo{"type": "decision", "title": "เลื่อน dashboard ไป sprint 16"})
	c.check("decision saved D-01", r["record"] == "D-01", r)
	write("update_record", jo{"code": "R-01", "status": "closed"})
	rec, _ := a.Records.GetByCode(ctx, a.ws(), "R-01")
	c.check("risk closed", rec != nil && rec.Status == "closed", rec)

	// milestones
	_, r = write("set_milestone", jo{"name": "งวดที่ 2", "due": dd(12), "value_percent": 30, "modules": []string{"Login"},
		"deliverables": []any{jo{"tor": "4.2", "name": "API login"}, jo{"tor": "6.1", "name": "คู่มือผู้ดูแลระบบ"}, jo{"tor": "4.5", "name": "Docs"}}})
	c.check("milestone saved M-01", r["record"] == "M-01", r)
	ps, _ := a.tasks().Projects(ctx)
	project := ps[0]
	ms, err := a.MilestonesFor(ctx, project, false)
	if err != nil || len(ms) == 0 {
		t.Fatalf("milestones: %v %v", ms, err)
	}
	sts := []string{}
	for _, d := range ms[0].Deliverables {
		sts = append(sts, d.Status)
	}
	c.check("deliverable statuses", strings.Join(sts, ",") == "open,missing,done", ms[0].Deliverables)
	c.check("module percent (Login 0/2)", ms[0].ModulePercent != nil && *ms[0].ModulePercent == 0, ms[0])
	c.check("milestone card builds", strings.Contains(toJSON(a.milestoneCard(project.Name, ms[0], nil)), "สร้างงานที่ยังขาด 1 งาน"))

	// change request
	pv, _ = a.PrepareWrite(ctx, "log_change_request", raw(jo{"title": "Export Excel", "requester": "คุณวิภา", "tasks": 5, "man_days": 20, "create_document": false}))
	hasImpact := false
	for _, l := range pv.Lines {
		hasImpact = hasImpact || l.Text == "ผลต่อกำหนดส่ง"
	}
	c.check("CR preview shows schedule impact", hasImpact, pv.Lines)
	if _, err := a.ExecuteWrite(ctx, pv.Plan); err != nil {
		t.Fatal(err)
	}
	rec, _ = a.Records.GetByCode(ctx, a.ws(), "CR-01")
	c.check("CR saved pending", rec != nil && rec.Status == "pending", rec)

	// budget + effort
	write("set_budget", jo{"total_man_days": 10, "modules": jo{"Login": 4}})
	write("log_effort", jo{"entries": []any{jo{"item_key": "FDA-1", "hours": 16, "who": "ton"}, jo{"item_key": "FDA-3", "hours": 8}}})
	b, err := a.BudgetStatus(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	c.check("budget used 3 md", b.UsedManDays == 3, b)
	login := -1.0
	for _, x := range b.Modules {
		if x.Name == "Login" {
			login = x.Used
		}
	}
	c.check("budget by module Login 2", login == 2, b.Modules)
	c.check("progress 25%", b.ProgressPercent == 25, b.ProgressPercent)
	c.check("budget card builds", strings.Contains(toJSON(a.budgetCard(b)), "งบแรงงาน"))

	// capacity (no Google)
	capa, err := a.Capacity(ctx, project, domain.StartOfWeek(dd(0)))
	if err != nil {
		t.Fatal(err)
	}
	hasTon := false
	for _, p := range capa.People {
		hasTon = hasTon || p.Name == "ton"
	}
	c.check("capacity has ton", hasTon, capa.People)

	// nudges
	pv, _ = a.PrepareWrite(ctx, "nudge_stale", raw(jo{}))
	n := 0
	for _, l := range pv.Lines {
		if strings.HasPrefix(l.Text, "FDA-") {
			n++
		}
	}
	c.check("nudge lists stale FDA-1 + FDA-2", n == 2, pv.Lines)
	if _, err := a.ExecuteWrite(ctx, pv.Plan); err != nil {
		t.Fatal(err)
	}
	c.check("nudge posted 2 comments", len(e.plane.comments) == 2, e.plane.comments)

	// reminders
	tr := &turn{}
	if _, err := a.RunReadTool(ctx, tr, "set_reminder", raw(jo{"at": dd(1) + "T10:00", "text": "ตาม FDA-1", "item_key": "FDA-1"})); err != nil {
		t.Fatal(err)
	}
	c.check("reminder card attached", len(tr.cards) == 1)
	rems, _ := a.records(ctx, domain.KindReminder, domain.ListFilter{Status: []string{"open"}})
	if len(rems) != 1 {
		t.Fatalf("reminders: %v", rems)
	}
	past := time.Now().Add(-time.Second)
	a.Records.Update(ctx, a.ws(), rems[0].ID, domain.RecordPatch{DueAt: &past, SetDueAt: true})
	due, _ := a.Records.DueReminders(ctx)
	c.check("due reminder found", len(due) == 1, due)
	var pushed [][]port.Message
	push := func(_ context.Context, m []port.Message) error { pushed = append(pushed, m); return nil }
	if err := a.fireReminders(ctx, push); err != nil {
		t.Fatal(err)
	}
	c.check("reminder pushed with item row", len(pushed) == 1 && strings.Contains(toJSON(pushed[0]), "FDA-1 API login"), pushed)
	due, _ = a.Records.DueReminders(ctx)
	c.check("reminder not sent twice", len(due) == 0, due)
	_, err = a.RunReadTool(ctx, tr, "set_reminder", raw(jo{"at": dd(-1) + "T10:00", "text": "x"}))
	c.check("past reminder rejected", err != nil)
	out := a.HandlePostback(ctx, "snooze:"+itoa(int(rems[0].ID)))
	c.check("snooze reopens", strings.Contains(toJSON(out), "เลื่อนไปเตือน"), out)

	// weekly report, retro
	wr, err := a.WeeklyReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	doneFDA4 := false
	for _, x := range wr.Done {
		doneFDA4 = doneFDA4 || x.Key == "FDA-4"
	}
	c.check("weekly report mentions project + done item", strings.Contains(wr.HTML, "FDA Big Data") && doneFDA4, wr)
	rt, err := a.Retro(ctx, project, nil)
	if err != nil {
		t.Fatal(err)
	}
	blockedMissed := false
	if rt != nil {
		for _, x := range rt.Improve {
			blockedMissed = blockedMissed || strings.Contains(x, "Blocked")
		}
	}
	c.check("retro of Sprint 0: 1/2 done, 1 blocked missed", rt != nil && rt.Cycle == "Sprint 0" && rt.Done == 1 && blockedMissed, rt)
	id, _ := a.WeeklyReportProactive(wr)
	c.check("weekly proactive saved", a.Store.Snapshot().Proactive[id] != nil)
	html, ok := a.PreviewPage(id)
	c.check("preview page shows the report", ok && strings.Contains(html, "FDA Big Data"))

	// dashboard APIs
	dr, err := a.DashboardRecords(ctx, "p1")
	c.check("records API", err == nil && dr["enabled"] == true && len(dr["risks"].([]domain.Record)) == 1, dr)
	dash, err := a.Dashboard(ctx, "")
	c.check("dashboard API", err == nil && dash["selected"] == "p1", dash)

	// settings
	snap := a.SettingsSnapshot(ctx)
	sj := toJSON(snap)
	c.check("settings snapshot tokenLast4", strings.Contains(sj, `"tokenLast4":"1234"`) && strings.Contains(sj, `"fromEnv":true`), snap["task"])
	c.check("snapshot never contains the token", !strings.Contains(sj, "envtoken1234"))
	_, err = a.PlaneTest(ctx, PlaneInput{BaseURL: "https://other.test", Workspace: "ws"})
	c.check("new server without token refused", err != nil && strings.Contains(err.Error(), "token ใหม่"), err)
	_, err = a.PlaneTest(ctx, PlaneInput{BaseURL: e.plane.URL + "/", Workspace: "ws"})
	c.check("same server reuses env token", err == nil, err)
	_, err = a.PlaneTest(ctx, PlaneInput{BaseURL: e.plane.URL, Workspace: "ws", Token: "bad"})
	c.check("bad token fails", err != nil)
	err = a.PlaneSave(ctx, PlaneInput{BaseURL: e.plane.URL, Workspace: "ws", Token: "newtoken9999", Projects: []string{"FDA"}})
	conn := a.Store.Connections().Task
	c.check("plane saved from settings", err == nil && conn.Provider == "plane" && conn.Plane != nil && conn.Plane.TokenLast4 == "9999", err, conn)
	c.check("saved token encrypted", !strings.Contains(toJSON(a.Store.Snapshot()), "newtoken9999"))
	_, err = a.SavePrefs([]byte(`{"digestTime":"25:00"}`))
	c.check("bad time rejected", err != nil)
	p, err := a.SavePrefs([]byte(`{"digestTime":"08:15","weekly":{"enabled":true,"day":5,"time":"16:00","to":"a@x.co, bad, b@y.co"}}`))
	c.check("prefs saved", err == nil && p.DigestTime == "08:15" && strings.Join(p.Weekly.To, ",") == "a@x.co,b@y.co", p)
	c.check("other prefs kept", p.StaleDays == 5 && p.Nudge.Day != 0, p)

	// built-in provider
	err = a.SetProvider(ctx, "builtin")
	c.check("switch to builtin", err == nil && a.tasks().Kind() == port.KindBuiltin && a.ws() == "builtin", err)
	bp, err := a.CreateBuiltinProject(ctx, "ERP Rollout", "erp")
	c.check("builtin project created", err == nil && bp.Identifier == "ERP", bp, err)
	a.Store.Update(func(s *domain.AppState) { s.CurrentProjectID = &bp.ID })
	_, r = write("create_work_items", jo{"items": []any{jo{"name": "Kickoff", "target_date": dd(2)}, jo{"name": "Setup", "state": "In Progress"}}})
	created, _ := r["created"].([]itemRef)
	ck := []string{}
	for _, x := range created {
		ck = append(ck, x.Key)
	}
	c.check("builtin items created ERP-1, ERP-2", strings.Join(ck, ",") == "ERP-1,ERP-2", r)
	write("update_work_item", jo{"key": "ERP-1", "state": "Done"})
	it, err := a.tasks().WorkItemByKey(ctx, "ERP-1")
	c.check("builtin update to done sets completedAt", err == nil && it.StateID == "done" && it.CompletedAt != nil, it)
	risks, _ := a.records(ctx, domain.KindRisk, domain.ListFilter{})
	c.check("risk log is per workspace (builtin empty)", len(risks) == 0, risks)

	// data summary / export / delete
	sum, _ := a.History.DataSummary(ctx)
	found := false
	for _, w := range sum {
		found = found || (w.WS == wsKey && w.Records >= 3)
	}
	c.check("data summary lists plane workspace", found, sum)
	dl, err := a.Export(ctx, wsKey, "records")
	c.check("CSV export has BOM + header", err == nil && strings.HasPrefix(string(dl.Body), "\ufeff") && strings.Contains(string(dl.Body), "R-01"), string(dl.Body))
	dl, err = a.Export(ctx, wsKey, "")
	c.check("JSON export", err == nil && json.Valid(dl.Body) && strings.HasSuffix(dl.Name, ".json"), err)
	c.check("workspace delete", a.DeleteData(ctx, "workspace", "", wsKey) == nil)
	sum, _ = a.History.DataSummary(ctx)
	gone := true
	for _, w := range sum {
		gone = gone && w.WS != wsKey
	}
	c.check("workspace data deleted", gone, sum)
	c.check("wipe needs typed confirm", a.DeleteData(ctx, "all", "ไม่", "") != nil)
	err = a.DeleteData(ctx, "all", "ลบทั้งหมด", "")
	c.check("wipe all", err == nil && a.Store.Connections().Task.Provider == "none" && a.tasks().Kind() == port.KindNone, err)
}

func TestStateAndHistoryPostgres(t *testing.T) {
	e := newEnv(t, metricsData(), true)
	a, c, ctx := e.app, checker{t}, context.Background()
	a.Store.Update(func(s *domain.AppState) { s.CurrentProjectID = ptr("p1") })
	st := e.db.State()
	time.Sleep(300 * time.Millisecond)
	doc, err := st.Load(ctx)
	var saved struct {
		CurrentProjectID string `json:"currentProjectId"`
	}
	_ = json.Unmarshal(doc, &saved)
	c.check("state saved to Postgres", err == nil && saved.CurrentProjectID == "p1", string(doc))
	ps, _ := a.tasks().Projects(ctx)
	m, _ := a.ProjectMetrics(ctx, ps[0])
	c.check("snapshot", a.Snapshot(ctx, m) == nil && a.Snapshot(ctx, m) == nil) // idempotent per day
	a.logActivity(ctx, "create_work_items", "สร้าง FDA-9 ทดสอบ", ptr("p1"), nil)
	a.History.LogUsage(ctx, "claude-haiku-5-5", 1000, 200, 500)
	time.Sleep(300 * time.Millisecond)
	h, err := a.HistoryData(ctx, "p1", 30)
	if err != nil {
		t.Fatal(err)
	}
	c.check("history has today's point", len(h.Points) == 1 && h.Points[0].Overdue == 1, h.Points)
	logged := false
	for _, x := range h.Activity {
		logged = logged || strings.Contains(x.Summary, "FDA-9")
	}
	c.check("activity logged", logged, h.Activity)
	c.check("usage logged", len(h.Usage) > 0 && h.Usage[0].Calls >= 1, h.Usage)
}
