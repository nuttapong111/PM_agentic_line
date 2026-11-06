package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

// turn collects cards a tool wants shown with the reply (slot picker, risk list…).
type turn struct{ cards []port.Message }

func (t *turn) attach(m port.Message) {
	if t != nil {
		t.cards = append(t.cards, m)
	}
}

func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return domain.UserError("input ไม่ถูกรูปแบบ: " + err.Error())
	}
	return nil
}

func compact(v domain.ItemView) map[string]any {
	m := map[string]any{"key": v.Key, "name": v.Name, "state": v.State, "assignees": v.Assignees, "priority": v.Priority, "target": v.Target}
	if v.DaysOverdue != 0 {
		m["days_overdue"] = v.DaysOverdue
	}
	return m
}

func compacts(vs []domain.ItemView, n int) []map[string]any {
	out := []map[string]any{}
	for _, v := range vs[:min(n, len(vs))] {
		out = append(out, compact(v))
	}
	return out
}

// RunReadTool runs a tool that changes nothing outside the bot (or only the bot's own state).
func (a *App) RunReadTool(ctx context.Context, t *turn, name string, raw json.RawMessage) (any, error) {
	tp := a.tasks()
	switch name {
	case "list_projects":
		ps, err := tp.Projects(ctx)
		if err != nil {
			return nil, err
		}
		cur := a.Store.CurrentProject()
		out := []map[string]any{}
		for _, p := range ps {
			out = append(out, map[string]any{"name": p.Name, "identifier": p.Identifier, "current": p.ID == cur})
		}
		return out, nil
	case "set_current_project":
		var in struct{ Project string }
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		p, err := a.ResolveProject(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		a.Store.Update(func(s *domain.AppState) { s.CurrentProjectID = &p.ID })
		return map[string]any{"ok": true, "current_project": p.Name}, nil
	case "project_status":
		var in struct{ Project string }
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		p, err := a.ResolveProject(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		m, err := a.ProjectMetrics(ctx, p)
		if err != nil {
			return nil, err
		}
		var cycle any
		if m.Cycle != nil {
			c := *m.Cycle
			c.Burndown = nil
			b, _ := json.Marshal(c)
			var cm map[string]any
			_ = json.Unmarshal(b, &cm)
			delete(cm, "burndown")
			cycle = cm
		}
		stale := []map[string]any{}
		for _, s := range m.Stale[:min(5, len(m.Stale))] {
			c := compact(s)
			c["days_stale"] = s.DaysStale
			stale = append(stale, c)
		}
		return map[string]any{
			"project": m.Project.Name, "today": m.Today, "counts": m.Counts, "cycle": cycle,
			"overdue": compacts(m.Overdue, 5), "blocked": compacts(m.Blocked, 5), "stale": stale,
			"due_today": compacts(m.DueToday, len(m.DueToday)), "workload": m.Workload, "modules": m.Modules, "bugs_by_priority": m.BugsByPriority,
		}, nil
	case "search_work_items":
		return a.searchWorkItems(ctx, raw)
	case "get_work_item":
		var in struct{ Key string }
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		p, err := a.ProjectOfKey(ctx, in.Key)
		if err != nil {
			return nil, err
		}
		d, err := a.LoadData(ctx, p)
		if err != nil {
			return nil, err
		}
		it, err := tp.WorkItemByKey(ctx, in.Key)
		if err != nil {
			return nil, err
		}
		out := compact(a.view(d, it))
		out["project"], out["start"], out["completed_at"], out["updated_at"], out["url"] = p.Name, it.StartDate, it.CompletedAt, it.UpdatedAt, tp.ItemURL(it)
		return out, nil
	case "list_members_and_states":
		var in struct{ Project string }
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		p, err := a.ResolveProject(ctx, in.Project)
		if err != nil {
			return nil, err
		}
		members, err := tp.Members(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		states, err := tp.States(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		names := []string{}
		for _, m := range members {
			names = append(names, m.Name())
		}
		st := []map[string]any{}
		for _, s := range states {
			st = append(st, map[string]any{"name": s.Name, "group": s.Group})
		}
		return map[string]any{"project": p.Name, "members": names, "nicknames": a.Store.Nicknames(), "states": st}, nil
	case "dashboard_link":
		var in struct{ Project string }
		_ = decode(raw, &in)
		id := ""
		if p := a.ProjectOrNil(ctx, in.Project); p != nil {
			id = p.ID
		}
		return map[string]any{"url": a.DashboardURL(id)}, nil
	}
	if a.toolNames[name] {
		return a.runFeatureRead(ctx, t, name, raw)
	}
	return nil, fmt.Errorf("unknown tool %s", name)
}

func (a *App) searchWorkItems(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		Project        string
		Text           string
		Assignee       string
		Status         string
		InCurrentCycle bool `json:"in_current_cycle"`
		Limit          int
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	p, err := a.ResolveProject(ctx, in.Project)
	if err != nil {
		return nil, err
	}
	d, err := a.LoadData(ctx, p)
	if err != nil {
		return nil, err
	}
	today := a.today()
	in7 := domain.DateIn(a.Now().AddDate(0, 0, 7), a.Loc)
	status := in.Status
	if status == "" {
		status = "open"
	}
	keep := func(it domain.WorkItem) bool {
		t := it.Target()
		open := d.IsOpen(it)
		switch status {
		case "open":
			return open
		case "overdue":
			return open && t != "" && t < today
		case "due_today":
			return open && t == today
		case "due_7_days":
			return open && t != "" && t >= today && t <= in7
		case "blocked":
			return open && d.IsBlocked(it)
		case "done":
			return d.Group(it) == domain.GroupCompleted
		}
		return true
	}
	var memberID string
	if in.Assignee != "" && in.Assignee != "none" {
		m, err := a.ResolveMember(ctx, p.ID, in.Assignee)
		if err != nil {
			return nil, err
		}
		if m != nil {
			memberID = m.ID
		}
	}
	var cycleIDs map[string]bool
	if in.InCurrentCycle {
		cycleIDs = map[string]bool{}
		if c := a.currentCycle(ctx, p.ID); c != nil {
			links, _ := a.tasks().CycleLinks(ctx, p.ID, c.ID)
			for _, l := range links {
				cycleIDs[l.IssueID] = true
			}
		}
	}
	q := strings.ToLower(in.Text)
	var items []domain.WorkItem
	for _, it := range d.Items {
		if !keep(it) || (q != "" && !strings.Contains(strings.ToLower(it.Name), q)) {
			continue
		}
		if in.Assignee == "none" && len(it.AssigneeIDs) > 0 {
			continue
		}
		if in.Assignee != "" && in.Assignee != "none" && (memberID == "" || !hasString(it.AssigneeIDs, memberID)) {
			continue
		}
		if cycleIDs != nil && !cycleIDs[it.ID] {
			continue
		}
		items = append(items, it)
	}
	orNine := func(it domain.WorkItem) string {
		if t := it.Target(); t != "" {
			return t
		}
		return "9999"
	}
	sort.SliceStable(items, func(i, j int) bool { return orNine(items[i]) < orNine(items[j]) })
	limit := in.Limit
	if limit == 0 {
		limit = 10
	}
	out := []map[string]any{}
	for _, it := range items[:min(limit, len(items))] {
		out = append(out, compact(a.view(d, it)))
	}
	return map[string]any{"project": p.Name, "total": len(items), "items": out}, nil
}

// ---------------------------------------------------------------- write tools

// PrepareWrite resolves a write tool call into a preview; nothing changes yet.
func (a *App) PrepareWrite(ctx context.Context, name string, raw json.RawMessage) (domain.Preview, error) {
	switch name {
	case "create_work_items":
		return a.prepareCreate(ctx, raw)
	case "update_work_item":
		return a.prepareUpdate(ctx, raw)
	case "add_comment":
		var in struct{ Key, Text string }
		if err := decode(raw, &in); err != nil {
			return domain.Preview{}, err
		}
		p, err := a.ProjectOfKey(ctx, in.Key)
		if err != nil {
			return domain.Preview{}, err
		}
		it, err := a.tasks().WorkItemByKey(ctx, in.Key)
		if err != nil {
			return domain.Preview{}, err
		}
		return domain.Preview{
			Title: "คอมเมนต์ใน " + it.Key, Subtitle: it.Name,
			Lines: []domain.PreviewLine{{Text: "ข้อความ", Sub: cut(in.Text, 300)}}, Warnings: []string{}, ConfirmLabel: "ยืนยัน ส่งคอมเมนต์",
			Plan: domain.WritePlan{Kind: domain.PlanComment, ProjectID: &p.ID, ItemID: it.ID, Key: it.Key, HTML: "<p>" + domain.EscapeHTML(in.Text) + "</p>"},
		}, nil
	}
	if a.writes[name] {
		return a.prepareFeatureWrite(ctx, name, raw)
	}
	return domain.Preview{}, fmt.Errorf("unknown write tool %s", name)
}

func (a *App) prepareCreate(ctx context.Context, raw json.RawMessage) (domain.Preview, error) {
	var in struct {
		Project           string
		AddToCurrentCycle *bool `json:"add_to_current_cycle"`
		Items             []struct {
			Name        string
			Description string
			Assignee    string
			Priority    string
			State       string
			StartDate   *string `json:"start_date"`
			TargetDate  *string `json:"target_date"`
			ParentIndex *int    `json:"parent_index"`
			Tag         string
		}
	}
	if err := decode(raw, &in); err != nil {
		return domain.Preview{}, err
	}
	p, err := a.ResolveProject(ctx, in.Project)
	if err != nil {
		return domain.Preview{}, err
	}
	states, err := a.tasks().States(ctx, p.ID)
	if err != nil {
		return domain.Preview{}, err
	}
	var cycle *domain.Cycle
	if in.AddToCurrentCycle == nil || *in.AddToCurrentCycle {
		cycle = a.currentCycle(ctx, p.ID)
	}
	warnings := []string{}
	items := []domain.PlanItem{}
	lines := []domain.PreviewLine{}
	for idx, it := range in.Items {
		if err := domain.CheckDate(it.StartDate, "start_date"); err != nil {
			return domain.Preview{}, err
		}
		if err := domain.CheckDate(it.TargetDate, "target_date"); err != nil {
			return domain.Preview{}, err
		}
		var m *domain.Member
		if it.Assignee != "" {
			if m, err = a.ResolveMember(ctx, p.ID, it.Assignee); err != nil {
				return domain.Preview{}, err
			}
		}
		var st *domain.State
		if it.State != "" {
			s, err := a.ResolveState(ctx, p.ID, it.State)
			if err != nil {
				return domain.Preview{}, err
			}
			st = &s
		}
		if it.ParentIndex != nil && *it.ParentIndex >= idx {
			return domain.Preview{}, domain.UserError("parent_index ต้องชี้ไปงานที่อยู่ก่อนหน้า")
		}
		w := domain.WorkItemWrite{Name: ptr(it.Name), StartDate: it.StartDate, TargetDate: it.TargetDate}
		if it.Description != "" {
			w.DescriptionHTML = ptr("<p>" + domain.EscapeHTML(it.Description) + "</p>")
		}
		if m != nil {
			w.Assignees = &[]string{m.ID}
		}
		if it.Priority != "" {
			w.Priority = ptr(it.Priority)
		}
		if st != nil {
			w.State = ptr(st.ID)
		}
		items = append(items, domain.PlanItem{WorkItemWrite: w, ParentIndex: it.ParentIndex})
		isParent := false
		for _, x := range in.Items {
			if x.ParentIndex != nil && *x.ParentIndex == idx {
				isParent = true
			}
		}
		tag := it.Tag
		if tag == "" && isParent {
			tag = "งานแม่"
		}
		who := "ยังไม่มีผู้รับผิดชอบ"
		if m != nil {
			who = m.Name()
		}
		due := ""
		if it.TargetDate != nil {
			due = "ครบ " + domain.ThaiDate(*it.TargetDate, true)
		}
		lines = append(lines, domain.PreviewLine{Tag: tag, Text: it.Name, Sub: joinNonEmpty(" · ", who, due), Warn: m == nil && !isParent})
		if m == nil && !isParent {
			warnings = append(warnings, fmt.Sprintf(`"%s" ยังไม่มีผู้รับผิดชอบ`, it.Name))
		}
	}
	var def *domain.State
	for i := range states {
		if states[i].Group == domain.GroupUnstarted {
			def = &states[i]
			break
		}
	}
	if def == nil && len(states) > 0 {
		def = &states[0]
	}
	title := fmt.Sprintf("%d งานใหม่", len(items))
	if len(items) == 1 {
		title = *items[0].Name
	}
	cycleName, defName := "", ""
	var cycleID *string
	if cycle != nil {
		cycleName, cycleID = cycle.Name, &cycle.ID
	}
	if def != nil {
		defName = def.Name
	}
	return domain.Preview{
		Title: title, Subtitle: joinNonEmpty(" · ", p.Name, cycleName, defName), Lines: lines, Warnings: warnings,
		ConfirmLabel: fmt.Sprintf("ยืนยัน สร้าง %d งาน", len(items)),
		Plan:         domain.WritePlan{Kind: domain.PlanCreate, ProjectID: &p.ID, CycleID: cycleID, Items: domain.MustJSON(items)},
	}, nil
}

func (a *App) prepareUpdate(ctx context.Context, raw json.RawMessage) (domain.Preview, error) {
	var in struct {
		Key, Name, State, Assignee, Priority string
		StartDate                            string `json:"start_date"`
		TargetDate                           string `json:"target_date"`
		MoveToCurrentCycle                   bool   `json:"move_to_current_cycle"`
	}
	if err := decode(raw, &in); err != nil {
		return domain.Preview{}, err
	}
	p, err := a.ProjectOfKey(ctx, in.Key)
	if err != nil {
		return domain.Preview{}, err
	}
	it, err := a.tasks().WorkItemByKey(ctx, in.Key)
	if err != nil {
		return domain.Preview{}, err
	}
	d, err := a.LoadData(ctx, p)
	if err != nil {
		return domain.Preview{}, err
	}
	body := domain.WorkItemWrite{}
	lines := []domain.PreviewLine{}
	if in.Name != "" {
		body.Name = ptr(in.Name)
		lines = append(lines, domain.PreviewLine{Text: "ชื่อ", Sub: it.Name + " → " + in.Name})
	}
	if in.State != "" {
		st, err := a.ResolveState(ctx, p.ID, in.State)
		if err != nil {
			return domain.Preview{}, err
		}
		body.State = ptr(st.ID)
		lines = append(lines, domain.PreviewLine{Text: "สถานะ", Sub: d.StateName(it) + " → " + st.Name})
	}
	if in.Assignee != "" {
		m, err := a.ResolveMember(ctx, p.ID, in.Assignee)
		if err != nil {
			return domain.Preview{}, err
		}
		after, ids := "ไม่มี", []string{}
		if m != nil {
			after, ids = m.Name(), []string{m.ID}
		}
		body.Assignees = &ids
		before := strings.Join(d.AssigneeNames(it), ", ")
		if before == "" {
			before = "ไม่มี"
		}
		lines = append(lines, domain.PreviewLine{Text: "ผู้รับผิดชอบ", Sub: before + " → " + after})
	}
	if in.Priority != "" {
		body.Priority = ptr(in.Priority)
		lines = append(lines, domain.PreviewLine{Text: "Priority", Sub: it.Priority + " → " + in.Priority})
	}
	if in.StartDate != "" {
		if err := domain.CheckDate(&in.StartDate, "start_date"); err != nil {
			return domain.Preview{}, err
		}
		body.StartDate = ptr(in.StartDate)
		lines = append(lines, domain.PreviewLine{Text: "วันเริ่ม", Sub: domain.ThaiDatePtr(it.StartDate, true) + " → " + domain.ThaiDate(in.StartDate, true)})
	}
	if in.TargetDate != "" {
		if err := domain.CheckDate(&in.TargetDate, "target_date"); err != nil {
			return domain.Preview{}, err
		}
		body.TargetDate = ptr(in.TargetDate)
		lines = append(lines, domain.PreviewLine{Text: "กำหนดส่ง", Sub: domain.ThaiDatePtr(it.TargetDate, true) + " → " + domain.ThaiDate(in.TargetDate, true)})
	}
	var cycleID *string
	if in.MoveToCurrentCycle {
		c := a.currentCycle(ctx, p.ID)
		if c == nil {
			return domain.Preview{}, domain.UserError("โปรเจกต์นี้ไม่มี cycle ที่กำลังเดินอยู่")
		}
		cycleID = &c.ID
		lines = append(lines, domain.PreviewLine{Text: "Cycle", Sub: "ย้ายเข้า " + c.Name})
	}
	if len(lines) == 0 {
		return domain.Preview{}, domain.UserError("ไม่ได้ระบุว่าจะแก้อะไร")
	}
	return domain.Preview{
		Title: it.Key + " " + it.Name, Subtitle: fmt.Sprintf("%s · แก้ไข %d รายการ", p.Name, len(lines)), Lines: lines, Warnings: []string{},
		ConfirmLabel: "ยืนยัน แก้ไข",
		Plan:         domain.WritePlan{Kind: domain.PlanUpdate, ProjectID: &p.ID, ItemID: it.ID, Key: it.Key, Body: &body, CycleID: cycleID},
	}, nil
}

// Result is what a confirmed write returns: shown to the model and turned into result links.
type Result = map[string]any

type itemRef struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// ExecuteWrite runs a confirmed plan.
func (a *App) ExecuteWrite(ctx context.Context, plan domain.WritePlan) (Result, error) {
	switch plan.Kind {
	case domain.PlanCreate, domain.PlanUpdate, domain.PlanComment:
	default:
		return a.executeFeature(ctx, plan)
	}
	tp := a.tasks()
	project, err := a.findProject(ctx, deref(plan.ProjectID))
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, &domain.StatusError{Status: 404, Msg: "project not found"}
	}
	switch plan.Kind {
	case domain.PlanCreate:
		created := []domain.WorkItem{}
		for _, it := range plan.CreateItems() {
			body := it.WorkItemWrite
			if it.ParentIndex != nil && *it.ParentIndex < len(created) {
				body.Parent = ptr(created[*it.ParentIndex].ID)
			}
			c, err := tp.CreateWorkItem(ctx, *project, body)
			if err != nil {
				return nil, err
			}
			created = append(created, c)
		}
		if plan.CycleID != nil {
			ids := make([]string, len(created))
			for i, c := range created {
				ids[i] = c.ID
			}
			if err := tp.AddToCycle(ctx, project.ID, *plan.CycleID, ids); err != nil {
				return nil, err
			}
		}
		refs := []itemRef{}
		for _, c := range created {
			refs = append(refs, itemRef{c.Key, c.Name, tp.ItemURL(c)})
		}
		return Result{"created": refs}, nil
	case domain.PlanUpdate:
		body := domain.WorkItemWrite{}
		if plan.Body != nil {
			body = *plan.Body
		}
		it, err := tp.UpdateWorkItem(ctx, *project, plan.ItemID, body)
		if err != nil {
			return nil, err
		}
		if plan.CycleID != nil {
			if err := tp.AddToCycle(ctx, project.ID, *plan.CycleID, []string{plan.ItemID}); err != nil {
				return nil, err
			}
		}
		return Result{"updated": itemRef{plan.Key, it.Name, tp.ItemURL(it)}}, nil
	}
	if err := tp.AddComment(ctx, *project, plan.ItemID, plan.HTML); err != nil {
		return nil, err
	}
	return Result{"commented": plan.Key}, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
