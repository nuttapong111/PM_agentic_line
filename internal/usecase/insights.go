package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
)

// Computed views on top of task data and the bot's own records: milestone/TOR status, man-day
// budget, team capacity, weekly report, retro, meeting prep. Numbers come from code.

func (a *App) records(ctx context.Context, kind domain.RecordKind, f domain.ListFilter) ([]domain.Record, error) {
	return a.Records.List(ctx, a.ws(), []domain.RecordKind{kind}, f)
}

// ---------------------------------------------------------------- milestones / TOR

type ModuleRef struct {
	Name    string `json:"name"`
	Percent int    `json:"percent"`
	Status  string `json:"status"`
}

type DeliverableStatus struct {
	Tor    string `json:"tor,omitempty"`
	Name   string `json:"name"`
	Status string `json:"status"` // done | open | missing
	Note   string `json:"note"`
	Key    string `json:"key,omitempty"`
}

type MilestoneStatus struct {
	ID            int64               `json:"id"`
	Code          *string             `json:"code"`
	Name          string              `json:"name"`
	Due           string              `json:"due"`
	DaysLeft      int                 `json:"daysLeft"`
	ValuePercent  *float64            `json:"valuePercent,omitempty"`
	ModulePercent *int                `json:"modulePercent"`
	Modules       []ModuleRef         `json:"modules"`
	Deliverables  []DeliverableStatus `json:"deliverables"`
	Done          int                 `json:"done"`
	Total         int                 `json:"total"`
	Status        string              `json:"status"`
}

func (a *App) milestoneStatus(rec domain.Record, m domain.ProjectMetrics, d *domain.ProjectData) MilestoneStatus {
	var md domain.MilestoneData
	rec.Decode(&md)
	var mods []domain.ModuleMetrics
	for _, name := range md.Modules {
		for _, x := range m.Modules {
			if domain.Norm(x.Name) == domain.Norm(name) || strings.Contains(domain.Norm(x.Name), domain.Norm(name)) {
				mods = append(mods, x)
				break
			}
		}
	}
	tot, dn := 0, 0
	refs := []ModuleRef{}
	for _, x := range mods {
		tot += x.Total
		dn += x.Done
		refs = append(refs, ModuleRef{x.Name, x.Percent, x.Status})
	}
	dels := []DeliverableStatus{}
	done := 0
	for _, del := range md.Deliverables {
		var it *domain.WorkItem
		for i := range d.Items {
			x := d.Items[i]
			if del.ItemKey != "" {
				if strings.EqualFold(x.Key, del.ItemKey) {
					it = &d.Items[i]
					break
				}
			} else if strings.Contains(domain.Norm(x.Name), domain.Norm(del.Name)) || strings.Contains(domain.Norm(del.Name), domain.Norm(x.Name)) {
				it = &d.Items[i]
				break
			}
		}
		switch {
		case it == nil:
			dels = append(dels, DeliverableStatus{Tor: del.Tor, Name: del.Name, Status: "missing", Note: "ยังไม่มีงานใน Plane"})
		case d.Group(*it) == domain.GroupCompleted:
			done++
			dels = append(dels, DeliverableStatus{Tor: del.Tor, Name: del.Name, Status: "done", Note: it.Key + " เสร็จแล้ว", Key: it.Key})
		default:
			name := ""
			if s, ok := d.States[it.StateID]; ok {
				name = s.Name
			}
			dels = append(dels, DeliverableStatus{Tor: del.Tor, Name: del.Name, Status: "open", Note: strings.TrimSpace(it.Key + " " + name), Key: it.Key})
		}
	}
	ms := MilestoneStatus{
		ID: rec.ID, Code: rec.Code, Name: md.Name, Due: md.Due, DaysLeft: domain.DaysBetween(a.today(), md.Due), ValuePercent: md.ValuePercent,
		Modules: refs, Deliverables: dels, Done: done, Total: len(dels), Status: rec.Status,
	}
	if tot > 0 {
		ms.ModulePercent = ptr(round(float64(dn) / float64(tot) * 100))
	}
	return ms
}

// MilestonesFor lists a project's milestones with progress, soonest first.
func (a *App) MilestonesFor(ctx context.Context, p domain.Project, includeDone bool) ([]MilestoneStatus, error) {
	f := domain.ListFilter{ProjectID: p.ID}
	if !includeDone {
		f.Status = []string{"open"}
	}
	recs, err := a.records(ctx, domain.KindMilestone, f)
	if err != nil || len(recs) == 0 {
		return []MilestoneStatus{}, err
	}
	d, err := a.LoadData(ctx, p)
	if err != nil {
		return nil, err
	}
	m := a.metricsFor(ctx, d)
	out := make([]MilestoneStatus, 0, len(recs))
	for _, r := range recs {
		out = append(out, a.milestoneStatus(r, m, d))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Due < out[j].Due })
	return out, nil
}

// ---------------------------------------------------------------- man-day budget

type ModuleBudget struct {
	Name   string   `json:"name"`
	Used   float64  `json:"used"`
	Budget *float64 `json:"budget"`
}

type BudgetStatus struct {
	Project          string         `json:"project"`
	HasBudget        bool           `json:"hasBudget"`
	TotalManDays     *float64       `json:"totalManDays"`
	UsedManDays      float64        `json:"usedManDays"`
	UsedPercent      *int           `json:"usedPercent"`
	ProgressPercent  int            `json:"progressPercent"`
	Entries          int            `json:"entries"`
	BurnAheadPercent *int           `json:"burnAheadPercent"`
	ProjectedTotal   *float64       `json:"projectedTotal"`
	ProjectedOverrun *float64       `json:"projectedOverrun"`
	Modules          []ModuleBudget `json:"modules"`
	Basis            string         `json:"basis"`
}

func (a *App) BudgetStatus(ctx context.Context, p domain.Project) (BudgetStatus, error) {
	budgets, err := a.records(ctx, domain.KindBudget, domain.ListFilter{ProjectID: p.ID, Limit: 1})
	if err != nil {
		return BudgetStatus{}, err
	}
	efforts, err := a.records(ctx, domain.KindEffort, domain.ListFilter{ProjectID: p.ID, Limit: 5000})
	if err != nil {
		return BudgetStatus{}, err
	}
	hours := a.Store.Prefs().ManDayHours
	if hours == 0 {
		hours = 8
	}
	d, err := a.LoadData(ctx, p)
	if err != nil {
		return BudgetStatus{}, err
	}
	live, done := 0, 0
	for _, it := range d.Items {
		g := d.Group(it)
		if g == domain.GroupCancelled {
			continue
		}
		live++
		if g == domain.GroupCompleted {
			done++
		}
	}
	progress := 0.0
	if live > 0 {
		progress = float64(done) / float64(live)
	}
	t := a.tasks()
	keyToModule := map[string]string{}
	if mods, err := t.Modules(ctx, p.ID); err == nil {
		for _, mod := range mods {
			ids, _ := t.ModuleItemIDs(ctx, p.ID, mod.ID)
			set := map[string]bool{}
			for _, id := range ids {
				set[id] = true
			}
			for _, it := range d.Items {
				if set[it.ID] {
					keyToModule[strings.ToUpper(it.Key)] = mod.Name
				}
			}
		}
	}
	usedHours := 0.0
	byModule := map[string]float64{}
	var order []string
	for _, e := range efforts {
		var ed domain.EffortData
		e.Decode(&ed)
		usedHours += ed.Hours
		mod := keyToModule[strings.ToUpper(ed.ItemKey)]
		if ed.ItemKey == "" || mod == "" {
			mod = "อื่น ๆ"
		}
		if _, ok := byModule[mod]; !ok {
			order = append(order, mod)
		}
		byModule[mod] += ed.Hours / hours
	}
	used := usedHours / hours
	var bd *domain.BudgetData
	if len(budgets) > 0 {
		bd = &domain.BudgetData{}
		budgets[0].Decode(bd)
	}
	out := BudgetStatus{
		Project: p.Name, HasBudget: bd != nil, UsedManDays: round1(used), ProgressPercent: round(progress * 100), Entries: len(efforts),
		Basis: fmt.Sprintf("คิดจาก effort ที่บันทึกผ่านบอท (%s ชม. = 1 man-day) และความคืบหน้าจากจำนวนงานที่เสร็จ", num(hours)),
	}
	var names []string
	seen := map[string]bool{}
	if bd != nil {
		out.TotalManDays = &bd.TotalManDays
		for k := range bd.Modules {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, n := range names {
			seen[n] = true
		}
	}
	for _, n := range order {
		if !seen[n] {
			names = append(names, n)
			seen[n] = true
		}
	}
	if out.TotalManDays != nil && *out.TotalManDays != 0 {
		usedPct := used / *out.TotalManDays
		out.UsedPercent = ptr(round(usedPct * 100))
		out.BurnAheadPercent = ptr(round((usedPct - progress) * 100))
	}
	if progress > 0.05 {
		projected := used / progress
		out.ProjectedTotal = ptr(round1(projected))
		if out.TotalManDays != nil && *out.TotalManDays != 0 {
			out.ProjectedOverrun = ptr(round1(projected - *out.TotalManDays))
		}
	}
	out.Modules = []ModuleBudget{}
	for _, n := range names {
		mb := ModuleBudget{Name: n, Used: round1(byModule[n])}
		if bd != nil {
			if v, ok := bd.Modules[n]; ok {
				mb.Budget = ptr(v)
			}
		}
		out.Modules = append(out.Modules, mb)
	}
	return out, nil
}

// ---------------------------------------------------------------- capacity

type CapacityItem struct {
	Key    string  `json:"key"`
	Name   string  `json:"name"`
	Target *string `json:"target"`
}

type CapacityRow struct {
	Name           string         `json:"name"`
	Leave          []string       `json:"leave"`
	WorkDays       int            `json:"workDays"`
	DueItems       int            `json:"dueItems"`
	CarriedOverdue int            `json:"carriedOverdue"`
	LoadPercent    int            `json:"loadPercent"`
	Items          []CapacityItem `json:"items"`
}

type Capacity struct {
	Project     string        `json:"project"`
	Week        string        `json:"week"`
	WeekLabel   string        `json:"weekLabel"`
	LeaveSource string        `json:"leaveSource"`
	Basis       string        `json:"basis"`
	Overloaded  []string      `json:"overloaded"`
	People      []CapacityRow `json:"people"`
}

func (a *App) Capacity(ctx context.Context, p domain.Project, weekStart string) (Capacity, error) {
	today := a.today()
	ws := weekStart
	if ws == "" {
		ws = domain.AddDays(domain.StartOfWeek(today), 7)
	}
	we := domain.AddDays(ws, 4)
	d, err := a.LoadData(ctx, p)
	if err != nil {
		return Capacity{}, err
	}
	prefs := a.Store.Prefs()
	people := []domain.Person{}
	for _, m := range d.MemberList {
		aliases := []string{m.DisplayName, m.FirstName, m.Email}
		for k, v := range prefs.Nicknames {
			if domain.Norm(v) == domain.Norm(m.Email) || domain.Norm(v) == domain.Norm(m.DisplayName) {
				aliases = append(aliases, k)
			}
		}
		people = append(people, domain.Person{ID: m.ID, Name: m.Name(), Aliases: aliases})
	}
	leave := map[string][]string{}
	source := "ไม่ได้ดึงวันลา (ยังไม่ได้ตั้งปฏิทินวันลาในหน้าตั้งค่า)"
	if prefs.LeaveCalendarID != "" && a.Google.Connected() {
		if l, err := a.Google.LeaveDays(ctx, prefs.LeaveCalendarID, ws, we, people); err == nil {
			leave, source = l, "ดึงวันลาจาก Google Calendar"
		} else {
			source = "ดึงวันลาไม่ได้: " + err.Error()
		}
	}
	from := ws
	if ws < today {
		from = today
	}
	rows := []CapacityRow{}
	for _, pr := range people {
		var due, overdue []domain.WorkItem
		for _, it := range d.Items {
			t := it.Target()
			if !d.IsOpen(it) || t == "" || !hasString(it.AssigneeIDs, pr.ID) {
				continue
			}
			if t < from {
				overdue = append(overdue, it)
			} else if t <= we {
				due = append(due, it)
			}
		}
		l := []string{}
		for _, day := range leave[pr.Name] {
			if w := domain.Weekday(day); w > 0 && w < 6 {
				l = append(l, day)
			}
		}
		days := max(0, 5-len(l))
		demand := len(due) + len(overdue)
		load := 0
		if days > 0 {
			load = round(float64(demand) / float64(days) * 100)
		} else if demand > 0 {
			load = 999
		}
		if demand == 0 && len(l) == 0 {
			continue
		}
		items := []CapacityItem{}
		for _, it := range append(overdue, due...) {
			items = append(items, CapacityItem{it.Key, it.Name, it.TargetDate})
		}
		rows = append(rows, CapacityRow{Name: pr.Name, Leave: l, WorkDays: days, DueItems: len(due), CarriedOverdue: len(overdue), LoadPercent: load, Items: items})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].LoadPercent > rows[j].LoadPercent })
	over := []string{}
	for _, r := range rows {
		if r.LoadPercent > 100 {
			over = append(over, r.Name)
		}
	}
	return Capacity{
		Project: p.Name, Week: ws + " ถึง " + we, WeekLabel: domain.ThaiDate(ws, false) + " – " + domain.ThaiDate(we, false), LeaveSource: source,
		Basis:      "นับงานที่ครบกำหนดในสัปดาห์นั้น (รวมงานค้างเลยกำหนด) เป็นงานละ 1 วัน เทียบกับวันทำงานหลังหักวันลา",
		Overloaded: over, People: rows,
	}, nil
}

func hasString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- weekly report

type ReportProject struct {
	Name    string `json:"name"`
	Percent *int   `json:"percent"`
	Delta   *int   `json:"delta"`
	Overdue int    `json:"overdue"`
	Blocked int    `json:"blocked"`
	Note    string `json:"note"`
}

type KeyName struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type WeeklyReport struct {
	Subject    string          `json:"subject"`
	HTML       string          `json:"html"`
	Week       int             `json:"week"`
	RangeLabel string          `json:"rangeLabel"`
	Projects   []ReportProject `json:"projects"`
	Done       []KeyName       `json:"done"`
	Next       []string        `json:"next"`
	Risks      []string        `json:"risks"`
}

func signed(n int) string {
	if n >= 0 {
		return fmt.Sprintf("+%d", n)
	}
	return fmt.Sprint(n)
}

func (p ReportProject) summary() string {
	s := ""
	if p.Percent != nil {
		s = fmt.Sprintf("เสร็จ %d%%", *p.Percent)
		if p.Delta != nil {
			s += fmt.Sprintf(" (%s%%)", signed(*p.Delta))
		}
	}
	return s
}

func (a *App) WeeklyReport(ctx context.Context) (WeeklyReport, error) {
	today := a.today()
	ws := domain.StartOfWeek(today)
	nextWs, nextWe := domain.AddDays(ws, 7), domain.AddDays(ws, 13)
	weekAgo := a.Now().Add(-7 * 24 * time.Hour)
	r := WeeklyReport{Projects: []ReportProject{}, Done: []KeyName{}, Next: []string{}, Risks: []string{}}
	projects, err := a.tasks().Projects(ctx)
	if err != nil {
		return r, err
	}
	for _, p := range projects {
		if err := a.weeklyProject(ctx, p, &r, weekAgo, nextWs, nextWe); err != nil {
			slog.Error("weekly report: project failed", "project", p.Identifier, "err", err)
		}
	}
	if a.Google.Connected() {
		if evs, err := a.Google.ListEvents(ctx, domain.LocalToTime(nextWs, "00:00", a.Loc), domain.LocalToTime(nextWe, "23:59", a.Loc), "primary", ""); err == nil {
			for _, e := range evs {
				if !e.AllDay && len(e.Attendees) > 1 {
					r.Next = append(r.Next, fmt.Sprintf("%s (%s)", e.Summary, domain.ThaiDate(e.Start, true)))
				}
			}
		}
	}
	r.Week = domain.ISOWeek(today)
	r.RangeLabel = domain.ThaiDate(ws, false) + " – " + domain.ThaiDate(domain.AddDays(ws, 6), false)
	r.Subject = fmt.Sprintf("Weekly status · สัปดาห์ที่ %d (%s)", r.Week, r.RangeLabel)
	li := func(xs []string) string {
		if len(xs) == 0 {
			return "<p>-</p>"
		}
		var b strings.Builder
		b.WriteString("<ul>")
		for _, x := range xs {
			b.WriteString("<li>" + domain.EscapeHTML(x) + "</li>")
		}
		b.WriteString("</ul>")
		return b.String()
	}
	var summary []string
	for _, p := range r.Projects {
		s := p.Name
		if sm := p.summary(); sm != "" {
			s += " " + sm
		}
		summary = append(summary, s+" · "+p.Note)
	}
	var doneLines []string
	for _, x := range r.Done[:min(30, len(r.Done))] {
		doneLines = append(doneLines, x.Key+" "+x.Name)
	}
	r.HTML = `<div style="font-family:Sarabun,Tahoma,sans-serif;font-size:14px;color:#18202B">
<h2 style="margin:0 0 4px">` + domain.EscapeHTML(r.Subject) + `</h2>
<h3>สรุปสัปดาห์</h3>` + li(summary) + `
<h3>เสร็จสัปดาห์นี้ ` + itoa(len(r.Done)) + ` งาน</h3>` + li(doneLines) + `
<h3>สัปดาห์หน้า</h3>` + li(r.Next) + `
<h3>ความเสี่ยงที่ต้องการความช่วยเหลือ</h3>` + li(r.Risks) + `
<p style="color:#5A6573;font-size:12px">ร่างจากข้อมูลในระบบจัดการงานโดย PM Assistant</p></div>`
	return r, nil
}

func (a *App) weeklyProject(ctx context.Context, p domain.Project, r *WeeklyReport, weekAgo time.Time, nextWs, nextWe string) error {
	d, err := a.LoadData(ctx, p)
	if err != nil {
		return err
	}
	m := a.metricsFor(ctx, d)
	before, _ := a.History.MetricsAround(ctx, p.ID, 7)
	rp := ReportProject{Name: p.Name, Overdue: m.Counts.Overdue, Blocked: m.Counts.Blocked}
	if m.Cycle != nil {
		rp.Percent = ptr(m.Cycle.Percent)
		if before != nil && before.CyclePercent != nil && before.CycleName != nil && *before.CycleName == m.Cycle.Name {
			rp.Delta = ptr(m.Cycle.Percent - *before.CyclePercent)
		}
	}
	var notes []string
	if m.Counts.Overdue > 0 {
		notes = append(notes, fmt.Sprintf("เลยกำหนด %d งาน", m.Counts.Overdue))
	}
	if m.Counts.Blocked > 0 {
		notes = append(notes, fmt.Sprintf("Blocked %d งาน", m.Counts.Blocked))
	}
	rp.Note = strings.Join(notes, ", ")
	if rp.Note == "" {
		rp.Note = "ตามแผน"
	}
	r.Projects = append(r.Projects, rp)
	for _, it := range d.Items {
		if it.CompletedAt != nil {
			if t, ok := domain.ParseInstant(*it.CompletedAt); ok && !t.Before(weekAgo) {
				r.Done = append(r.Done, KeyName{it.Key, it.Name})
			}
		}
	}
	for _, mod := range m.Modules {
		if mod.Target != nil && *mod.Target >= nextWs && *mod.Target <= nextWe && mod.Status != domain.ModDone {
			r.Next = append(r.Next, fmt.Sprintf("ส่งมอบ module %s · %s (%s)", mod.Name, p.Name, domain.ThaiDate(*mod.Target, true)))
		}
	}
	ms, err := a.records(ctx, domain.KindMilestone, domain.ListFilter{ProjectID: p.ID, Status: []string{"open"}})
	if err != nil {
		return err
	}
	for _, rec := range ms {
		var md domain.MilestoneData
		rec.Decode(&md)
		if md.Due >= nextWs && md.Due <= nextWe {
			r.Next = append(r.Next, fmt.Sprintf("%s · %s (%s)", md.Name, p.Name, domain.ThaiDate(md.Due, true)))
		}
	}
	risks, err := a.records(ctx, domain.KindRisk, domain.ListFilter{ProjectID: p.ID, Status: []string{"open"}})
	if err != nil {
		return err
	}
	for _, rec := range risks {
		var rd domain.RiskData
		rec.Decode(&rd)
		if rd.Impact == domain.High {
			s := rec.CodeOr("") + " " + rd.Title
			if rd.Mitigation != "" {
				s += " · แผนรองรับ: " + rd.Mitigation
			}
			r.Risks = append(r.Risks, s)
		}
	}
	return nil
}

// ---------------------------------------------------------------- retro

func (a *App) LastEndedCycle(ctx context.Context, p domain.Project) *domain.Cycle {
	today := a.today()
	cycles, err := a.tasks().Cycles(ctx, p.ID)
	if err != nil {
		return nil
	}
	var best *domain.Cycle
	for i := range cycles {
		c := cycles[i]
		if c.EndDate == nil || c.StartDate == nil || cut(*c.EndDate, 10) > today {
			continue
		}
		if best == nil || *c.EndDate > *best.EndDate {
			best = &cycles[i]
		}
	}
	return best
}

type Retro struct {
	CycleID      string   `json:"cycleId"`
	Cycle        string   `json:"cycle"`
	Project      string   `json:"project"`
	Start        string   `json:"start"`
	End          string   `json:"end"`
	Total        int      `json:"total"`
	Done         int      `json:"done"`
	Percent      int      `json:"percent"`
	AddedMidway  int      `json:"addedMidway"`
	ScopePercent int      `json:"scopePercent"`
	Good         []string `json:"good"`
	Improve      []string `json:"improve"`
	Topics       []string `json:"topics"`
	Missed       []string `json:"missed"`
	Markdown     string   `json:"markdown,omitempty"`
}

// Retro drafts a retrospective of a cycle (the last ended one when c is nil). nil = no ended cycle.
func (a *App) Retro(ctx context.Context, p domain.Project, c *domain.Cycle) (*Retro, error) {
	if c == nil {
		c = a.LastEndedCycle(ctx, p)
	}
	if c == nil {
		return nil, nil
	}
	d, err := a.LoadData(ctx, p)
	if err != nil {
		return nil, err
	}
	cm := a.CycleMetrics(ctx, d, c)
	if cm == nil {
		return nil, nil
	}
	links, _ := a.tasks().CycleLinks(ctx, p.ID, c.ID)
	ids := map[string]bool{}
	for _, l := range links {
		ids[l.IssueID] = true
	}
	var missed []domain.WorkItem
	blockedMissed, late := 0, 0
	for _, it := range d.Items {
		if !ids[it.ID] || d.Group(it) == domain.GroupCancelled {
			continue
		}
		if d.Group(it) != domain.GroupCompleted {
			missed = append(missed, it)
			if d.IsBlocked(it) {
				blockedMissed++
			}
		}
		if it.CompletedAt != nil && it.TargetDate != nil {
			if t, ok := domain.ParseInstant(*it.CompletedAt); ok && domain.DateIn(t, a.Loc) > *it.TargetDate {
				late++
			}
		}
	}
	scope := 0
	if cm.Initial > 0 {
		scope = round(float64(cm.AddedMidway) / float64(cm.Initial) * 100)
	}
	good, improve, topics := []string{}, []string{}, []string{}
	if cm.Percent >= 80 {
		good = append(good, fmt.Sprintf("เสร็จ %d%% ของ sprint", cm.Percent))
	}
	if cm.Done > 0 {
		good = append(good, fmt.Sprintf("ปิดงานได้ %d งาน", cm.Done))
	}
	if late == 0 && cm.Done > 0 {
		good = append(good, "งานที่เสร็จส่งทันกำหนดทั้งหมด")
	}
	if len(missed) > 0 {
		s := fmt.Sprintf("%d งานหลุด sprint", len(missed))
		if blockedMissed > 0 {
			s += fmt.Sprintf(", %d ใน %d ติด Blocked", blockedMissed, len(missed))
		}
		improve = append(improve, s)
	}
	if late > 0 {
		improve = append(improve, fmt.Sprintf("%d งานเสร็จช้ากว่ากำหนด", late))
	}
	if scope >= 15 {
		improve = append(improve, fmt.Sprintf("scope เพิ่มกลาง sprint %d งาน (%d%%)", cm.AddedMidway, scope))
	}
	if blockedMissed > 0 {
		topics = append(topics, "จะจัดการงานที่ติดปัจจัยภายนอกยังไง เช่น แยกออกจาก sprint หรือมีแผนสำรอง")
	}
	if scope >= 15 {
		topics = append(topics, "จะกันงานที่เข้ามากลาง sprint ยังไง")
	}
	if late > 0 {
		topics = append(topics, "ประเมินเวลางานให้แม่นขึ้นได้ยังไง")
	}
	if len(topics) == 0 {
		topics = append(topics, "อะไรที่ทำให้ sprint นี้ไปได้ดี และจะทำต่อยังไง")
	}
	md := []string{
		"# Retro · " + c.Name,
		p.Name + " · " + domain.ThaiDate(cm.Start, false) + " – " + domain.ThaiDate(cm.End, false),
		"## ผลลัพธ์",
		fmt.Sprintf("- เสร็จ %d / %d งาน (%d%%)", cm.Done, cm.Total, cm.Percent),
		fmt.Sprintf("- Scope เพิ่มกลาง sprint +%d งาน (%d%%)", cm.AddedMidway, scope),
		"## ที่ไปได้ดี",
	}
	bullets := func(xs []string) {
		for _, x := range xs {
			md = append(md, "- "+x)
		}
	}
	bullets(good)
	md = append(md, "## ที่ต้องปรับ")
	bullets(improve)
	md = append(md, "## หัวข้อชวนคุย")
	bullets(topics)
	keys := []string{}
	for _, it := range missed {
		keys = append(keys, it.Key)
	}
	if len(missed) > 0 {
		md = append(md, "## งานที่หลุด")
		for _, it := range missed[:min(20, len(missed))] {
			md = append(md, "- "+it.Key+" "+it.Name)
		}
	}
	return &Retro{
		CycleID: c.ID, Cycle: c.Name, Project: p.Name, Start: cm.Start, End: cm.End, Total: cm.Total, Done: cm.Done, Percent: cm.Percent,
		AddedMidway: cm.AddedMidway, ScopePercent: scope, Good: good, Improve: improve, Topics: topics, Missed: keys, Markdown: strings.Join(md, "\n"),
	}, nil
}

// ---------------------------------------------------------------- meeting prep

// ProjectForText guesses which project a meeting title is about.
func (a *App) ProjectForText(ctx context.Context, text string) *domain.Project {
	ps, err := a.tasks().Projects(ctx)
	if err != nil {
		return nil
	}
	t := domain.Norm(text)
	for i, p := range ps {
		if strings.Contains(t, domain.Norm(p.Identifier)) || strings.Contains(t, domain.Norm(p.Name)) {
			return &ps[i]
		}
	}
	for i, p := range ps {
		if f := strings.Fields(p.Name); len(f) > 0 && strings.Contains(t, domain.Norm(f[0])) {
			return &ps[i]
		}
	}
	cur := a.Store.CurrentProject()
	for i, p := range ps {
		if p.ID == cur {
			return &ps[i]
		}
	}
	return nil
}

type MeetingPrep struct {
	Event       domain.CalEvent `json:"event"`
	Project     *string         `json:"project"`
	Progress    *string         `json:"progress"`
	OpenActions []string        `json:"openActions"`
	DoneActions []string        `json:"doneActions"`
	Risks       []string        `json:"risks"`
	Crs         []string        `json:"crs"`
	Decisions   []string        `json:"decisions"`
	NotionURL   string          `json:"notionUrl,omitempty"`
}

var todoLine = regexp.MustCompile(`^\[( |x)\]\s+(.*)`)

func (a *App) MeetingPrep(ctx context.Context, ev domain.CalEvent) (MeetingPrep, error) {
	out := MeetingPrep{Event: ev, OpenActions: []string{}, DoneActions: []string{}, Risks: []string{}, Crs: []string{}, Decisions: []string{}}
	if p := a.ProjectForText(ctx, ev.Summary); p != nil {
		out.Project = &p.Name
		m, err := a.ProjectMetrics(ctx, *p)
		if err != nil {
			return out, err
		}
		var prog string
		if c := m.Cycle; c != nil {
			prog = fmt.Sprintf("%s เสร็จ %d%% · เลยกำหนด %d งาน", c.Name, c.Percent, m.Counts.Overdue)
			if m.Counts.Blocked > 0 {
				prog += fmt.Sprintf(" · Blocked %d", m.Counts.Blocked)
			}
		} else {
			prog = fmt.Sprintf("งานเปิด %d · เลยกำหนด %d", m.Counts.Open, m.Counts.Overdue)
		}
		out.Progress = &prog
		risks, _ := a.records(ctx, domain.KindRisk, domain.ListFilter{ProjectID: p.ID, Status: []string{"open"}})
		crs, _ := a.records(ctx, domain.KindCR, domain.ListFilter{ProjectID: p.ID, Status: []string{"pending"}})
		decisions, _ := a.records(ctx, domain.KindDecision, domain.ListFilter{ProjectID: p.ID, Limit: 3})
		for _, r := range risks {
			var rd domain.RiskData
			r.Decode(&rd)
			if rd.Impact != domain.Low && len(out.Risks) < 3 {
				out.Risks = append(out.Risks, fmt.Sprintf("%s %s (ผลกระทบ%s)", r.CodeOr(""), rd.Title, domain.LevelTH[rd.Impact]))
			}
		}
		for _, r := range crs[:min(3, len(crs))] {
			out.Crs = append(out.Crs, r.CodeOr("")+" "+r.TitleOrName()+" · รออนุมัติ")
		}
		for _, r := range decisions {
			out.Decisions = append(out.Decisions, r.CodeOr("")+" "+r.TitleOrName())
		}
	}
	if a.Notion.Connected() {
		if err := a.prepFromNotion(ctx, ev, &out); err != nil {
			slog.Error("meeting prep notion failed", "err", err)
		}
	}
	return out, nil
}

func (a *App) prepFromNotion(ctx context.Context, ev domain.CalEvent, out *MeetingPrep) error {
	pages, err := a.Notion.Search(ctx, ev.Summary, 5)
	if err != nil {
		return err
	}
	start := ev.StartTime()
	for _, pg := range pages {
		if t, ok := domain.ParseInstant(pg.EditedAt); !ok || !t.Before(start) {
			continue
		}
		text, err := a.Notion.PageText(ctx, pg.ID, 4000)
		if err != nil {
			return err
		}
		out.NotionURL = text.URL
		for _, l := range strings.Split(text.Text, "\n") {
			if m := todoLine.FindStringSubmatch(l); m != nil {
				if m[1] == "x" {
					out.DoneActions = append(out.DoneActions, m[2])
				} else {
					out.OpenActions = append(out.OpenActions, m[2])
				}
			}
		}
		break
	}
	return nil
}
