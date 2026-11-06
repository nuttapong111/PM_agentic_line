package domain

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Project metrics: every number the dashboard, digest and project_status show is computed
// here in code from the task data, never by the model.

type ItemView struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	State       string   `json:"state"`
	StateGroup  string   `json:"stateGroup"`
	Assignees   []string `json:"assignees"`
	Priority    string   `json:"priority"`
	Target      *string  `json:"target"`
	DaysOverdue int      `json:"daysOverdue,omitempty"`
	DaysStale   int      `json:"daysStale,omitempty"`
	URL         string   `json:"url"`
}

type Counts struct {
	Open         int `json:"open"`
	Overdue      int `json:"overdue"`
	Blocked      int `json:"blocked"`
	DoneThisWeek int `json:"doneThisWeek"`
	UrgentBugs   int `json:"urgentBugs"`
	Unassigned   int `json:"unassigned"`
}

type Workload struct {
	Name        string `json:"name"`
	Open        int    `json:"open"`
	DueThisWeek int    `json:"dueThisWeek"`
	Todo        int    `json:"todo"`
	InProgress  int    `json:"inProgress"`
	Blocked     int    `json:"blocked"`
	DueNext7    int    `json:"dueNext7"`
}

type BugCount struct {
	Priority string `json:"priority"`
	Count    int    `json:"count"`
}

type BurnPoint struct {
	Date      string  `json:"date"`
	Remaining int     `json:"remaining"`
	Ideal     float64 `json:"ideal"`
}

type CycleMetrics struct {
	Name          string      `json:"name"`
	Start         string      `json:"start"`
	End           string      `json:"end"`
	DaysLeft      int         `json:"daysLeft"`
	Total         int         `json:"total"`
	Done          int         `json:"done"`
	Percent       int         `json:"percent"`
	Initial       int         `json:"initial"`
	AddedMidway   int         `json:"addedMidway"`
	ProjectedMiss int         `json:"projectedMiss"`
	Burndown      []BurnPoint `json:"burndown"`
}

// Module progress status, ordered from worst to best.
const (
	ModLate    = "late"
	ModAtRisk  = "at_risk"
	ModWatch   = "watch"
	ModOnTrack = "on_track"
	ModDone    = "done"
)

type ModuleMetrics struct {
	Name    string  `json:"name"`
	Total   int     `json:"total"`
	Done    int     `json:"done"`
	Percent int     `json:"percent"`
	Target  *string `json:"target"`
	Start   *string `json:"start"`
	Status  string  `json:"status"`
}

type ProjectRef struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Identifier string `json:"identifier"`
	URL        string `json:"url"`
}

type ProjectMetrics struct {
	Project        ProjectRef      `json:"project"`
	Today          string          `json:"today"`
	Counts         Counts          `json:"counts"`
	Overdue        []ItemView      `json:"overdue"`
	DueToday       []ItemView      `json:"dueToday"`
	DueNext7       []ItemView      `json:"dueNext7"`
	Blocked        []ItemView      `json:"blocked"`
	Stale          []ItemView      `json:"stale"`
	Workload       []Workload      `json:"workload"`
	BugsByPriority []BugCount      `json:"bugsByPriority"`
	Cycle          *CycleMetrics   `json:"cycle"`
	Modules        []ModuleMetrics `json:"modules"`
}

// ProjectData is everything about one project the metrics need.
type ProjectData struct {
	Project Project
	Items   []WorkItem
	States  map[string]State
	Members map[string]Member
	// MemberList keeps the members in the order the task tool returned them.
	MemberList []Member
	Labels     []Label
}

// NewProjectData indexes states and members by id.
func NewProjectData(p Project, items []WorkItem, states []State, members []Member, labels []Label) *ProjectData {
	d := &ProjectData{Project: p, Items: items, States: map[string]State{}, Members: map[string]Member{}, MemberList: members, Labels: labels}
	for _, s := range states {
		d.States[s.ID] = s
	}
	for _, m := range members {
		d.Members[m.ID] = m
	}
	return d
}

func (d *ProjectData) Group(it WorkItem) StateGroup { return d.States[it.StateID].Group }

func (d *ProjectData) StateName(it WorkItem) string {
	if s, ok := d.States[it.StateID]; ok {
		return s.Name
	}
	return "?"
}

func (d *ProjectData) IsOpen(it WorkItem) bool {
	g := d.Group(it)
	return g != GroupCompleted && g != GroupCancelled
}

func (d *ProjectData) IsBlocked(it WorkItem) bool { return IsBlockedName(d.States[it.StateID].Name) }

func (d *ProjectData) MemberName(id string) string {
	if m, ok := d.Members[id]; ok {
		return m.Name()
	}
	return "?"
}

func (d *ProjectData) AssigneeNames(it WorkItem) []string {
	out := make([]string, 0, len(it.AssigneeIDs))
	for _, id := range it.AssigneeIDs {
		out = append(out, d.MemberName(id))
	}
	return out
}

// ByID indexes items by id.
func (d *ProjectData) ByID() map[string]WorkItem {
	m := make(map[string]WorkItem, len(d.Items))
	for _, it := range d.Items {
		m[it.ID] = it
	}
	return m
}

// View turns an item into what the owner sees.
func (d *ProjectData) View(it WorkItem, today string, url string) ItemView {
	st, ok := d.States[it.StateID]
	name, group := "?", "?"
	if ok {
		name, group = st.Name, string(st.Group)
	}
	v := ItemView{Key: it.Key, Name: it.Name, State: name, StateGroup: group, Assignees: d.AssigneeNames(it), Priority: it.Priority, Target: it.TargetDate, URL: url}
	if t := it.Target(); t != "" && t < today && d.IsOpen(it) {
		v.DaysOverdue = DaysBetween(t, today)
	}
	return v
}

// MetricsInput is the rest of what the metrics need besides ProjectData.
type MetricsInput struct {
	Now       time.Time
	Loc       *time.Location
	StaleDays int
	BugLabel  string
	ItemURL   func(WorkItem) string
	URL       string // project URL
	Cycle     *Cycle // current cycle, if any
	CycleRows []CycleLink
	Modules   []ModuleItems
}

// ModuleItems is a module with the ids of its items.
type ModuleItems struct {
	Module  Module
	ItemIDs []string
}

const day = 24 * time.Hour

// ComputeMetrics builds the project overview.
func ComputeMetrics(d *ProjectData, in MetricsInput) ProjectMetrics {
	today := DateIn(in.Now, in.Loc)
	view := func(it WorkItem) ItemView { return d.View(it, today, in.ItemURL(it)) }
	var open []WorkItem
	for _, it := range d.Items {
		if d.IsOpen(it) {
			open = append(open, it)
		}
	}
	byTarget := func(xs []WorkItem) {
		sort.SliceStable(xs, func(i, j int) bool { return xs[i].Target() < xs[j].Target() })
	}
	in7 := DateIn(in.Now.Add(7*day), in.Loc)
	var overdue, dueToday, dueNext7, blocked []WorkItem
	for _, it := range open {
		t := it.Target()
		switch {
		case t != "" && t < today:
			overdue = append(overdue, it)
		case t == today:
			dueToday = append(dueToday, it)
		case t != "" && t > today && t <= in7:
			dueNext7 = append(dueNext7, it)
		}
		if d.IsBlocked(it) {
			blocked = append(blocked, it)
		}
	}
	byTarget(overdue)
	byTarget(dueNext7)

	weekStart := StartOfWeek(today)
	doneThisWeek := 0
	for _, it := range d.Items {
		if it.CompletedAt != nil {
			if t, ok := ParseInstant(*it.CompletedAt); ok && DateIn(t, in.Loc) >= weekStart {
				doneThisWeek++
			}
		}
	}

	staleCut := in.Now.Add(-time.Duration(in.StaleDays) * day)
	type staleItem struct {
		it      WorkItem
		updated time.Time
	}
	var stale []staleItem
	for _, it := range open {
		if d.Group(it) != GroupStarted && !d.IsBlocked(it) {
			continue
		}
		if u, ok := ParseInstant(it.UpdatedAt); ok && u.Before(staleCut) {
			stale = append(stale, staleItem{it, u})
		}
	}
	sort.SliceStable(stale, func(i, j int) bool { return stale[i].it.UpdatedAt < stale[j].it.UpdatedAt })
	staleViews := make([]ItemView, 0, len(stale))
	for _, s := range stale {
		v := view(s.it)
		v.DaysStale = int(math.Floor(in.Now.Sub(s.updated).Hours() / 24))
		staleViews = append(staleViews, v)
	}

	// workload, in first-seen order then by open count
	weekEnd := AddDays(weekStart, 6)
	load := map[string]*Workload{}
	var order []string
	for _, it := range open {
		names := d.AssigneeNames(it)
		if len(names) == 0 {
			names = []string{"ไม่มีเจ้าของ"}
		}
		for _, n := range names {
			w, ok := load[n]
			if !ok {
				w = &Workload{Name: n}
				load[n] = w
				order = append(order, n)
			}
			w.Open++
			t := it.Target()
			if t != "" && t <= weekEnd {
				w.DueThisWeek++
			}
			if t != "" && t >= today && t <= AddDays(today, 7) {
				w.DueNext7++
			}
			switch {
			case d.IsBlocked(it):
				w.Blocked++
			case d.Group(it) == GroupStarted:
				w.InProgress++
			default:
				w.Todo++
			}
		}
	}
	workload := make([]Workload, 0, len(order))
	for _, n := range order {
		workload = append(workload, *load[n])
	}
	sort.SliceStable(workload, func(i, j int) bool { return workload[i].Open > workload[j].Open })

	// bugs
	bugIDs := map[string]bool{}
	for _, l := range d.Labels {
		if strings.EqualFold(l.Name, in.BugLabel) {
			bugIDs[l.ID] = true
		}
	}
	var bugs []WorkItem
	for _, it := range open {
		for _, l := range it.LabelIDs {
			if bugIDs[l] {
				bugs = append(bugs, it)
				break
			}
		}
	}
	var bugsByPriority []BugCount
	urgentBugs := 0
	for _, p := range []string{"urgent", "high", "medium", "low", "none"} {
		n := 0
		for _, b := range bugs {
			if b.Priority == p {
				n++
			}
		}
		if p == "urgent" || p == "high" {
			urgentBugs += n
		}
		if n > 0 || p != "none" {
			bugsByPriority = append(bugsByPriority, BugCount{p, n})
		}
	}
	unassigned := 0
	for _, it := range open {
		if len(it.AssigneeIDs) == 0 {
			unassigned++
		}
	}

	m := ProjectMetrics{
		Project: ProjectRef{ID: d.Project.ID, Name: d.Project.Name, Identifier: d.Project.Identifier, URL: in.URL},
		Today:   today,
		Counts: Counts{
			Open: len(open), Overdue: len(overdue), Blocked: len(blocked), DoneThisWeek: doneThisWeek,
			UrgentBugs: urgentBugs, Unassigned: unassigned,
		},
		Overdue: views(overdue, view), DueToday: views(dueToday, view), DueNext7: views(dueNext7, view),
		Blocked: views(blocked, view), Stale: staleViews, Workload: workload, BugsByPriority: bugsByPriority,
		Modules: ComputeModules(d, in.Modules, today),
	}
	if in.Cycle != nil {
		m.Cycle = ComputeCycle(d, *in.Cycle, in.CycleRows, in.Now, in.Loc)
	}
	return m
}

func views(xs []WorkItem, f func(WorkItem) ItemView) []ItemView {
	out := make([]ItemView, 0, len(xs))
	for _, x := range xs {
		out = append(out, f(x))
	}
	return out
}

// CurrentCycle finds the running cycle: the one Plane marks current, else the one spanning today.
func CurrentCycle(cycles []Cycle, today string) *Cycle {
	for i := range cycles {
		if strings.EqualFold(cycles[i].Status, "current") {
			return &cycles[i]
		}
	}
	for i := range cycles {
		c := cycles[i]
		if c.StartDate != nil && c.EndDate != nil && first10(*c.StartDate) <= today && first10(*c.EndDate) >= today {
			return &cycles[i]
		}
	}
	return nil
}

func first10(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

// ComputeCycle builds progress, scope change and burndown for a cycle.
func ComputeCycle(d *ProjectData, c Cycle, links []CycleLink, now time.Time, loc *time.Location) *CycleMetrics {
	if c.StartDate == nil || c.EndDate == nil {
		return nil
	}
	today := DateIn(now, loc)
	start, end := first10(*c.StartDate), first10(*c.EndDate)
	byID := d.ByID()
	type row struct {
		item  WorkItem
		added string
		done  string // local date completed, "" if not
	}
	var rows []row
	for _, l := range links {
		it, ok := byID[l.IssueID]
		if !ok || d.Group(it) == GroupCancelled {
			continue
		}
		added := today
		if t, ok := ParseInstant(l.AddedAt); ok {
			added = DateIn(t, loc)
		}
		doneDate := ""
		if it.CompletedAt != nil {
			if t, ok := ParseInstant(*it.CompletedAt); ok {
				doneDate = DateIn(t, loc)
			}
		}
		rows = append(rows, row{it, added, doneDate})
	}
	total, done, initial := len(rows), 0, 0
	for _, r := range rows {
		if d.Group(r.item) == GroupCompleted {
			done++
		}
		if r.added <= start {
			initial++
		}
	}
	span := max(1, DaysBetween(start, end))
	lastDay := end
	if today < end {
		lastDay = today
	}
	burndown := []BurnPoint{}
	for i := 0; i <= DaysBetween(start, lastDay); i++ {
		date := AddDays(start, i)
		scope, doneBy := 0, 0
		for _, r := range rows {
			if r.added <= date {
				scope++
				if r.done != "" && r.done <= date {
					doneBy++
				}
			}
		}
		ideal := math.Max(0, math.Round(float64(initial)*(1-float64(i)/float64(span))*10)/10)
		burndown = append(burndown, BurnPoint{Date: date, Remaining: scope - doneBy, Ideal: ideal})
	}
	elapsed := max(1, DaysBetween(start, lastDay))
	velocity := float64(done) / float64(elapsed)
	daysLeft := max(0, DaysBetween(today, end))
	projectedMiss := int(math.Max(0, math.Round(float64(total-done)-velocity*float64(daysLeft))))
	pct := 0
	if total > 0 {
		pct = int(math.Round(float64(done) / float64(total) * 100))
	}
	return &CycleMetrics{
		Name: c.Name, Start: start, End: end, DaysLeft: daysLeft, Total: total, Done: done, Percent: pct,
		Initial: initial, AddedMidway: total - initial, ProjectedMiss: projectedMiss, Burndown: burndown,
	}
}

var modRank = map[string]int{ModLate: 0, ModAtRisk: 1, ModWatch: 2, ModOnTrack: 3, ModDone: 4}

// ComputeModules rates each module's progress against its schedule.
func ComputeModules(d *ProjectData, mods []ModuleItems, today string) []ModuleMetrics {
	byID := d.ByID()
	out := []ModuleMetrics{}
	for _, mi := range mods {
		m := mi.Module
		total, done := 0, 0
		for _, id := range mi.ItemIDs {
			it, ok := byID[id]
			if !ok || d.Group(it) == GroupCancelled {
				continue
			}
			total++
			if d.Group(it) == GroupCompleted {
				done++
			}
		}
		pct := 0.0
		if total > 0 {
			pct = float64(done) / float64(total)
		}
		var target *string
		if m.TargetDate != nil {
			target = Ptr(first10(*m.TargetDate))
		}
		status := ModOnTrack
		switch {
		case total > 0 && done == total:
			status = ModDone
		case target != nil && *target < today:
			status = ModLate
		case target != nil && m.StartDate != nil:
			span := max(1, DaysBetween(*m.StartDate, *target))
			expected := math.Min(1, math.Max(0, float64(DaysBetween(*m.StartDate, today))/float64(span)))
			if pct < expected-0.25 {
				status = ModAtRisk
			} else if pct < expected-0.1 {
				status = ModWatch
			}
		}
		out = append(out, ModuleMetrics{Name: m.Name, Total: total, Done: done, Percent: int(math.Round(pct * 100)), Target: target, Start: m.StartDate, Status: status})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if modRank[out[i].Status] != modRank[out[j].Status] {
			return modRank[out[i].Status] < modRank[out[j].Status]
		}
		return orNine(out[i].Target) < orNine(out[j].Target)
	})
	return out
}

func orNine(s *string) string {
	if s == nil {
		return "9"
	}
	return *s
}
