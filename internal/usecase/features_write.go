package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

func preview(kicker, title, subtitle string, lines []domain.PreviewLine, confirm string, plan domain.WritePlan) domain.Preview {
	if lines == nil {
		lines = []domain.PreviewLine{}
	}
	return domain.Preview{Kicker: kicker, Title: title, Subtitle: subtitle, Lines: lines, Warnings: []string{}, ConfirmLabel: confirm, Plan: plan}
}

func (a *App) prepareFeatureWrite(ctx context.Context, name string, raw json.RawMessage) (domain.Preview, error) {
	none := domain.Preview{}
	switch name {
	case "log_risk":
		var in struct {
			Project, Type, Title, Detail, Impact, Likelihood, Owner, Mitigation, Source string
		}
		if err := decode(raw, &in); err != nil {
			return none, err
		}
		if err := a.needDB(); err != nil {
			return none, err
		}
		p, err := a.ResolveProject(ctx, in.Project)
		if err != nil {
			return none, err
		}
		var lines []domain.PreviewLine
		if in.Detail != "" {
			lines = append(lines, domain.PreviewLine{Text: "รายละเอียด", Sub: in.Detail})
		}
		if in.Type == "decision" {
			if in.Source != "" {
				lines = append(lines, domain.PreviewLine{Text: "ที่มา", Sub: in.Source})
			}
			data := domain.DecisionData{Title: in.Title, Detail: in.Detail, Source: in.Source, Date: a.today()}
			return preview("บันทึกการตัดสินใจ", in.Title, p.Name, lines, "ยืนยัน บันทึก",
				domain.WritePlan{Kind: domain.PlanRecordAdd, RecKind: domain.KindDecision, ProjectID: &p.ID, Data: domain.MustJSON(data)}), nil
		}
		data := domain.RiskData{Title: in.Title, Detail: in.Detail, Impact: domain.LevelOf(in.Impact), Likelihood: domain.LevelOf(in.Likelihood), Owner: in.Owner, Mitigation: in.Mitigation}
		if data.Owner == "" {
			data.Owner = "คุณ (PM)"
		}
		mit := data.Mitigation
		if mit == "" {
			mit = "ยังไม่มี"
		}
		lines = append(lines,
			domain.PreviewLine{Text: "ผลกระทบ / โอกาส", Sub: domain.LevelTH[data.Impact] + " / " + domain.LevelTH[data.Likelihood], Warn: data.Impact == domain.High},
			domain.PreviewLine{Text: "ผู้ดูแล", Sub: data.Owner},
			domain.PreviewLine{Text: "แผนรองรับ", Sub: mit, Warn: data.Mitigation == ""},
		)
		return preview("บันทึกความเสี่ยง · รอยืนยัน", in.Title, p.Name, lines, "ยืนยัน บันทึก",
			domain.WritePlan{Kind: domain.PlanRecordAdd, RecKind: domain.KindRisk, ProjectID: &p.ID, Data: domain.MustJSON(data)}), nil

	case "update_record":
		var in struct{ Code, Status, Note, Milestone string }
		if err := decode(raw, &in); err != nil {
			return none, err
		}
		if err := a.needDB(); err != nil {
			return none, err
		}
		r, err := a.Records.GetByCode(ctx, a.ws(), in.Code)
		if err != nil {
			return none, err
		}
		if r == nil {
			return none, domain.UserError("ไม่พบรายการ " + in.Code)
		}
		data := map[string]any{}
		var lines []domain.PreviewLine
		if in.Status != "" {
			lines = append(lines, domain.PreviewLine{Text: "สถานะ", Sub: domain.CrStatusTH(r.Status) + " → " + domain.CrStatusTH(in.Status)})
		}
		if in.Note != "" {
			data["note"] = in.Note
			lines = append(lines, domain.PreviewLine{Text: "หมายเหตุ", Sub: in.Note})
		}
		if in.Milestone != "" && r.Kind == domain.KindCR {
			m, err := a.findMilestone(ctx, deref(r.ProjectID), in.Milestone)
			if err != nil {
				return none, err
			}
			data["milestoneId"] = m.ID
			lines = append(lines, domain.PreviewLine{Text: "งวด", Sub: "ย้ายไป " + m.TitleOrName()})
		}
		if len(lines) == 0 {
			return none, domain.UserError("ไม่ได้ระบุว่าจะเปลี่ยนอะไร")
		}
		return preview("แก้รายการ", r.CodeOr("")+" "+r.TitleOrName(), "", lines, "ยืนยัน แก้ไข", domain.WritePlan{
			Kind: domain.PlanRecordUpdate, ID: r.ID, Code: r.CodeOr(fmt.Sprint(r.ID)), Status: in.Status, Data: domain.MustJSON(data),
		}), nil

	case "log_change_request":
		return a.prepareCR(ctx, raw)

	case "set_milestone":
		var in struct {
			Project, Code, Name, Due string
			ValuePercent             *float64 `json:"value_percent"`
			Modules                  []string
			Deliverables             []struct {
				Tor, Name string
				ItemKey   string `json:"item_key"`
				ItemKey2  string `json:"itemKey"`
			}
		}
		if err := decode(raw, &in); err != nil {
			return none, err
		}
		if err := a.needDB(); err != nil {
			return none, err
		}
		p, err := a.ResolveProject(ctx, in.Project)
		if err != nil {
			return none, err
		}
		if err := domain.CheckDate(&in.Due, "due"); err != nil {
			return none, err
		}
		var existing *domain.Record
		var old domain.MilestoneData
		if in.Code != "" {
			if existing, err = a.Records.GetByCode(ctx, a.ws(), in.Code); err != nil {
				return none, err
			}
			if existing == nil {
				return none, domain.UserError("ไม่พบงวด " + in.Code)
			}
			existing.Decode(&old)
		}
		data := domain.MilestoneData{Name: in.Name, Due: in.Due, ValuePercent: in.ValuePercent, Modules: in.Modules, Deliverables: []domain.Deliverable{}}
		if data.Modules == nil {
			data.Modules = old.Modules
		}
		if data.Modules == nil {
			data.Modules = []string{}
		}
		if in.Deliverables != nil {
			for _, d := range in.Deliverables {
				k := d.ItemKey
				if k == "" {
					k = d.ItemKey2
				}
				data.Deliverables = append(data.Deliverables, domain.Deliverable{Tor: d.Tor, Name: d.Name, ItemKey: k})
			}
		} else if old.Deliverables != nil {
			data.Deliverables = old.Deliverables
		}
		lines := []domain.PreviewLine{{Text: "กำหนดส่งมอบ", Sub: fmt.Sprintf("%s (อีก %d วัน)", domain.ThaiDate(data.Due, true), domain.DaysBetween(a.today(), data.Due))}}
		if data.ValuePercent != nil && *data.ValuePercent != 0 {
			lines = append(lines, domain.PreviewLine{Text: "มูลค่างวด", Sub: num(*data.ValuePercent) + "% ของสัญญา"})
		}
		if len(data.Modules) > 0 {
			lines = append(lines, domain.PreviewLine{Text: "Module", Sub: strings.Join(data.Modules, ", ")})
		}
		for _, d := range data.Deliverables[:min(6, len(data.Deliverables))] {
			tag := ""
			if d.Tor != "" {
				tag = cut("TOR "+d.Tor, 8)
			}
			sub := d.ItemKey
			if sub == "" {
				sub = "จับคู่กับงานจากชื่อ"
			}
			lines = append(lines, domain.PreviewLine{Tag: tag, Text: d.Name, Sub: sub})
		}
		kicker := "เพิ่มงวดงาน"
		var replace int64
		if existing != nil {
			kicker, replace = "แก้งวดงาน", existing.ID
		}
		return preview(kicker, data.Name, p.Name, lines, "ยืนยัน บันทึกงวด", domain.WritePlan{
			Kind: domain.PlanRecordAdd, RecKind: domain.KindMilestone, ProjectID: &p.ID, Data: domain.MustJSON(data), ReplaceID: replace,
		}), nil

	case "set_budget":
		var in struct {
			Project      string
			TotalManDays float64 `json:"total_man_days"`
			Modules      map[string]float64
		}
		if err := decode(raw, &in); err != nil {
			return none, err
		}
		if err := a.needDB(); err != nil {
			return none, err
		}
		p, err := a.ResolveProject(ctx, in.Project)
		if err != nil {
			return none, err
		}
		data := domain.BudgetData{TotalManDays: in.TotalManDays, Modules: in.Modules}
		if data.Modules == nil {
			data.Modules = map[string]float64{}
		}
		names := make([]string, 0, len(data.Modules))
		for k := range data.Modules {
			names = append(names, k)
		}
		sort.Strings(names)
		var lines []domain.PreviewLine
		for _, k := range names {
			lines = append(lines, domain.PreviewLine{Text: k, Sub: num(data.Modules[k]) + " man-day"})
		}
		return preview("ตั้งงบ man-day", fmt.Sprintf("%s · %s man-day", p.Name, num(data.TotalManDays)), p.Name, lines, "ยืนยัน ตั้งงบ",
			domain.WritePlan{Kind: domain.PlanRecordAdd, RecKind: domain.KindBudget, ProjectID: &p.ID, Data: domain.MustJSON(data)}), nil

	case "log_effort":
		var in struct {
			Entries []struct {
				Who     string
				ItemKey string `json:"item_key"`
				Project string
				Hours   float64
				Date    string
				Note    string
			}
		}
		if err := decode(raw, &in); err != nil {
			return none, err
		}
		if err := a.needDB(); err != nil {
			return none, err
		}
		entries := []domain.EffortEntry{}
		var lines []domain.PreviewLine
		total := 0.0
		for _, e := range in.Entries {
			var p domain.Project
			var err error
			if e.ItemKey != "" {
				p, err = a.ProjectOfKey(ctx, e.ItemKey)
			} else {
				p, err = a.ResolveProject(ctx, e.Project)
			}
			if err != nil {
				return none, err
			}
			if e.Date != "" {
				if err := domain.CheckDate(&e.Date, "date"); err != nil {
					return none, err
				}
			}
			who := "ฉัน"
			if e.Who != "" {
				m, err := a.ResolveMember(ctx, p.ID, e.Who)
				if err != nil {
					return none, err
				}
				who = m.Name()
			}
			date := e.Date
			if date == "" {
				date = a.today()
			}
			entries = append(entries, domain.EffortEntry{ProjectID: p.ID, Data: domain.EffortData{Who: who, ItemKey: strings.ToUpper(e.ItemKey), Hours: e.Hours, Date: date, Note: e.Note}})
			lines = append(lines, domain.PreviewLine{Text: fmt.Sprintf("%s · %s ชม.", who, num(e.Hours)), Sub: joinNonEmpty(" · ", e.ItemKey, p.Name, domain.ThaiDate(date, true))})
			total += e.Hours
		}
		return preview("บันทึกเวลาทำงาน", num(total)+" ชั่วโมง", fmt.Sprintf("%d รายการ", len(entries)), lines, "ยืนยัน บันทึก",
			domain.WritePlan{Kind: domain.PlanEffort, Entries: entries}), nil

	case "schedule_meeting":
		var in struct {
			Project, Title, Start, End, Agenda string
			Attendees                          []string
		}
		if err := decode(raw, &in); err != nil {
			return none, err
		}
		if err := a.needGoogle(); err != nil {
			return none, err
		}
		s, ok1 := domain.ParseLocal(in.Start, a.Loc)
		e, ok2 := domain.ParseLocal(in.End, a.Loc)
		if !ok1 || !ok2 || !e.After(s) {
			return none, domain.UserError("เวลาเริ่มหรือจบไม่ถูกต้อง ใช้ YYYY-MM-DDTHH:MM")
		}
		var p *domain.Project
		if in.Project != "" {
			pr, err := a.ResolveProject(ctx, in.Project)
			if err != nil {
				return none, err
			}
			p = &pr
		} else {
			p = a.ProjectOrNil(ctx, "")
		}
		pid, pname := "", ""
		if p != nil {
			pid, pname = p.ID, p.Name
		}
		list := []string{}
		var err error
		if len(in.Attendees) > 0 {
			if list, err = a.emails(ctx, pid, in.Attendees); err != nil {
				return none, err
			}
		} else if p != nil {
			if list, _, err = a.projectMemberEmails(ctx, pid); err != nil {
				return none, err
			}
		}
		who := cut(strings.Join(list, ", "), 300)
		if who == "" {
			who = "ไม่มี"
		}
		lines := []domain.PreviewLine{
			{Text: "เวลา", Sub: fmt.Sprintf("%s %s – %s", domain.ThaiDate(domain.DateIn(s, a.Loc), true), domain.HM(s, a.Loc), domain.HM(e, a.Loc))},
			{Text: fmt.Sprintf("ผู้เข้าร่วม %d คน", len(list)), Sub: who},
		}
		if in.Agenda != "" {
			lines = append(lines, domain.PreviewLine{Text: "วาระ", Sub: cut(in.Agenda, 300)})
		}
		return preview("นัดประชุม Google Meet · รอยืนยัน", in.Title, pname, lines, fmt.Sprintf("ยืนยัน นัดและส่ง invite %d คน", len(list)), domain.WritePlan{
			Kind: domain.PlanMeeting, Summary: in.Title, Description: in.Agenda, StartISO: domain.LocalISO(s, a.Loc), EndISO: domain.LocalISO(e, a.Loc), Attendees: list,
		}), nil

	case "send_email":
		var in struct {
			To, Cc                 []string
			Subject, Body, Project string
			NotionPageID           string `json:"notion_page_id"`
		}
		if err := decode(raw, &in); err != nil {
			return none, err
		}
		if err := a.needGoogle(); err != nil {
			return none, err
		}
		p := a.ProjectOrNil(ctx, in.Project)
		pid := ""
		var projectID *string
		if p != nil {
			pid, projectID = p.ID, &p.ID
		}
		to, err := a.emails(ctx, pid, in.To)
		if err != nil {
			return none, err
		}
		cc, err := a.emails(ctx, pid, in.Cc)
		if err != nil {
			return none, err
		}
		html := domain.TextToHTML(in.Body)
		from := a.Google.Email()
		if from == "" {
			from = "บัญชี Google ของคุณ"
		}
		lines := []domain.PreviewLine{{Text: "ถึง", Sub: strings.Join(to, ", ")}}
		if len(cc) > 0 {
			lines = append(lines, domain.PreviewLine{Text: "สำเนา", Sub: strings.Join(cc, ", ")})
		}
		lines = append(lines, domain.PreviewLine{Text: "เนื้อหา", Sub: cut(in.Body, 400)})
		pv := preview("ร่างอีเมล · รอตรวจ", in.Subject, "จาก "+from, lines, "ส่งอีเมล", domain.WritePlan{
			Kind: domain.PlanEmail, To: to, Cc: cc, Subject: in.Subject, HTML: html, NotionPageID: in.NotionPageID, ProjectID: projectID,
		})
		pv.FullHTML = html
		return pv, nil

	case "save_to_notion":
		var in struct {
			Title, Content string
			AppendTo       string `json:"append_to_page_id"`
		}
		if err := decode(raw, &in); err != nil {
			return none, err
		}
		if err := a.needNotion(); err != nil {
			return none, err
		}
		if in.AppendTo == "" && a.Store.Prefs().NotionParentPageID == "" {
			return none, domain.UserError("ยังไม่ได้ตั้งหน้า Notion สำหรับเก็บเอกสาร ตั้งได้ที่หน้าตั้งค่า > การแจ้งเตือน")
		}
		sub := "สร้างหน้าใหม่"
		if in.AppendTo != "" {
			sub = "เพิ่มต่อท้ายหน้าเดิม"
		}
		return preview("บันทึกลง Notion", in.Title, sub, []domain.PreviewLine{{Text: "เนื้อหา", Sub: cut(in.Content, 400)}}, "ยืนยัน บันทึกลง Notion",
			domain.WritePlan{Kind: domain.PlanNotionPage, Title: in.Title, Markdown: in.Content, AppendTo: in.AppendTo}), nil

	case "nudge_stale":
		var in struct {
			Project, Message string
			Keys             []string
		}
		if err := decode(raw, &in); err != nil {
			return none, err
		}
		p, err := a.ResolveProject(ctx, in.Project)
		if err != nil {
			return none, err
		}
		return a.NudgePreview(ctx, p.ID, in.Keys, in.Message)
	}
	return none, fmt.Errorf("unknown write tool %s", name)
}

func (a *App) prepareCR(ctx context.Context, raw json.RawMessage) (domain.Preview, error) {
	var in struct {
		Project, Title, Detail, Requester, Milestone string
		RequestedOn                                  string   `json:"requested_on"`
		Tasks                                        *int     `json:"tasks"`
		ManDays                                      *float64 `json:"man_days"`
		CreateDocument                               *bool    `json:"create_document"`
	}
	none := domain.Preview{}
	if err := decode(raw, &in); err != nil {
		return none, err
	}
	if err := a.needDB(); err != nil {
		return none, err
	}
	p, err := a.ResolveProject(ctx, in.Project)
	if err != nil {
		return none, err
	}
	ms, err := a.MilestonesFor(ctx, p, false)
	if err != nil {
		return none, err
	}
	var target *MilestoneStatus
	if len(ms) > 0 {
		target = &ms[0]
	}
	if in.Milestone != "" {
		m, err := a.findMilestone(ctx, p.ID, in.Milestone)
		if err != nil {
			return none, err
		}
		target = nil
		for i := range ms {
			if ms[i].ID == m.ID {
				target = &ms[i]
			}
		}
	}
	var lines []domain.PreviewLine
	warnings := []string{}
	if in.Detail != "" {
		lines = append(lines, domain.PreviewLine{Text: "รายละเอียด", Sub: in.Detail})
	}
	if in.Requester != "" {
		sub := in.Requester
		if in.RequestedOn != "" {
			sub += " · " + domain.ThaiDate(in.RequestedOn, false)
		}
		lines = append(lines, domain.PreviewLine{Text: "ผู้ขอ", Sub: sub})
	}
	tasks, manDays := 0, 0.0
	if in.Tasks != nil {
		tasks = *in.Tasks
	}
	if in.ManDays != nil {
		manDays = *in.ManDays
	}
	if tasks != 0 || manDays != 0 {
		var parts []string
		if tasks != 0 {
			parts = append(parts, fmt.Sprintf("%d งาน", tasks))
		}
		if manDays != 0 {
			parts = append(parts, "ประมาณ "+num(manDays)+" man-day")
		}
		lines = append(lines, domain.PreviewLine{Text: "ประเมินงานเพิ่ม", Sub: strings.Join(parts, " · ")})
	}
	impact := ""
	if target != nil && manDays != 0 {
		members, _ := a.tasks().Members(ctx, p.ID)
		team := max(1, len(members))
		shift := int(math.Ceil(manDays / float64(team)))
		workdaysLeft := max(0, round(float64(target.DaysLeft)*5/7))
		tight := workdaysLeft < shift+3
		remainingKnown := target.ModulePercent != nil && 100-*target.ModulePercent > 0
		if remainingKnown && tight {
			impact = fmt.Sprintf("%s อาจเลื่อน %d วัน", target.Name, shift)
		} else {
			impact = fmt.Sprintf("%s ใช้เวลาเพิ่มราว %d วันทำการ (ทีม %d คน)", target.Name, shift, team)
		}
		lines = append(lines, domain.PreviewLine{Text: "ผลต่อกำหนดส่ง", Sub: impact, Warn: tight})
		if tight {
			w := fmt.Sprintf("ถ้าอนุมัติตอนนี้ %s อาจเลยวันส่งมอบ %s", target.Name, domain.ThaiDate(target.Due, false))
			for _, x := range ms {
				if x.Due > target.Due {
					w += " ทางเลือก: ทำใน" + x.Name + "แทน"
					break
				}
			}
			warnings = append(warnings, w)
		}
	}
	notionOK := a.Notion.Connected()
	wantDoc := notionOK
	if in.CreateDocument != nil {
		wantDoc = *in.CreateDocument
	}
	doc := wantDoc && notionOK && a.Store.Prefs().NotionParentPageID != ""
	data := domain.CrData{Title: in.Title, Detail: in.Detail, Requester: in.Requester, RequestedOn: in.RequestedOn, Tasks: in.Tasks, ManDays: in.ManDays, ImpactNote: impact}
	if target != nil {
		data.MilestoneID = &target.ID
	}
	q := func(p *int) string {
		if p == nil {
			return "?"
		}
		return itoa(*p)
	}
	qf := func(p *float64) string {
		if p == nil {
			return "?"
		}
		return num(*p)
	}
	requester := in.Requester
	if requester == "" {
		requester = "-"
	}
	on := ""
	if in.RequestedOn != "" {
		on = "(" + in.RequestedOn + ")"
	}
	impactLine := ""
	if impact != "" {
		impactLine = "- " + impact
	}
	md := joinNonEmpty("\n",
		"# Change request: "+in.Title, "โปรเจกต์ "+p.Name, in.Detail,
		"## ผู้ขอ", "- "+requester+" "+on,
		"## ประเมิน", "- "+q(in.Tasks)+" งาน · "+qf(in.ManDays)+" man-day", impactLine,
		"## สถานะ", "- รออนุมัติ",
	)
	plan := domain.WritePlan{Kind: domain.PlanRecordAdd, RecKind: domain.KindCR, ProjectID: &p.ID, Data: domain.MustJSON(data), Status: "pending"}
	label := "ยืนยัน บันทึก CR"
	if doc {
		plan.NotionDoc = &domain.NotionDoc{Title: "CR: " + in.Title, Markdown: md}
		label = "บันทึก CR และร่างเอกสาร"
	}
	pv := preview("Change request · รอยืนยัน", in.Title, p.Name, append(lines, domain.PreviewLine{Text: "สถานะ", Sub: "รออนุมัติ"}), label, plan)
	pv.Warnings = warnings
	return pv, nil
}

func (a *App) findMilestone(ctx context.Context, projectID, ref string) (*domain.Record, error) {
	if r, err := a.Records.GetByCode(ctx, a.ws(), ref); err != nil {
		return nil, err
	} else if r != nil && r.Kind == domain.KindMilestone {
		return r, nil
	}
	all, err := a.records(ctx, domain.KindMilestone, domain.ListFilter{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	q := strings.Join(strings.Fields(ref), "")
	var names []string
	for i, m := range all {
		n := m.TitleOrName()
		if strings.Contains(strings.Join(strings.Fields(n), ""), q) {
			return &all[i], nil
		}
		names = append(names, m.CodeOr("")+" "+n)
	}
	list := strings.Join(names, ", ")
	if list == "" {
		list = "ยังไม่มีงวด"
	}
	return nil, domain.UserError(fmt.Sprintf(`ไม่พบงวด "%s" ที่มี: %s`, ref, list))
}

// NudgePreview drafts comments asking for updates on stale work.
func (a *App) NudgePreview(ctx context.Context, projectID string, keys []string, message string) (domain.Preview, error) {
	none := domain.Preview{}
	project, err := a.findProject(ctx, projectID)
	if err != nil {
		return none, err
	}
	if project == nil {
		return none, domain.UserError("ไม่พบโปรเจกต์นี้แล้ว")
	}
	d, err := a.LoadData(ctx, *project)
	if err != nil {
		return none, err
	}
	m := a.metricsFor(ctx, d)
	picks := m.Stale
	if len(keys) > 0 {
		want := map[string]bool{}
		var order []string
		for _, k := range keys {
			k = strings.ToUpper(k)
			if !want[k] {
				want[k] = true
				order = append(order, k)
			}
		}
		picks = nil
		for _, s := range m.Stale {
			if want[s.Key] {
				picks = append(picks, s)
			}
		}
		for _, k := range order {
			found := false
			for _, x := range picks {
				if x.Key == k {
					found = true
				}
			}
			if found {
				continue
			}
			for _, it := range d.Items {
				if it.Key == k {
					v := a.view(d, it)
					if u, ok := domain.ParseInstant(it.UpdatedAt); ok {
						v.DaysStale = int(math.Floor(a.Now().Sub(u).Hours() / 24))
					}
					picks = append(picks, v)
				}
			}
		}
	}
	staleDays := a.Store.Prefs().StaleDays
	if len(picks) == 0 {
		return none, domain.UserError(fmt.Sprintf("ไม่มีงานที่นิ่งเกิน %d วันใน %s", staleDays, project.Name))
	}
	picks = picks[:min(10, len(picks))]
	if message == "" {
		message = "ขออัปเดตความคืบหน้าหน่อยครับ ติดอะไรบอกได้เลย"
	}
	targets := []domain.PlanTarget{}
	lines := []domain.PreviewLine{}
	for _, s := range picks {
		for _, it := range d.Items {
			if it.Key == s.Key {
				targets = append(targets, domain.PlanTarget{ItemID: it.ID, Key: s.Key})
				break
			}
		}
		who := strings.Join(s.Assignees, ", ")
		if who == "" {
			who = "ไม่มีเจ้าของ"
		}
		lines = append(lines, domain.PreviewLine{Text: s.Key + " " + s.Name, Sub: fmt.Sprintf("%s · นิ่งมา %d วัน", who, s.DaysStale)})
	}
	lines = append(lines, domain.PreviewLine{Text: "ข้อความที่จะคอมเมนต์", Sub: "“" + message + "”"})
	return domain.Preview{
		Kicker: "ทวงอัปเดต · รอยืนยัน", Title: fmt.Sprintf("งานนิ่งเกิน %d วัน %d งาน", staleDays, len(picks)),
		Subtitle: project.Name + " · ถ้าไม่ต้องการทวงบางงาน กดแก้ไขแล้วบอกได้", Lines: lines, Warnings: []string{},
		ConfirmLabel: fmt.Sprintf("ยืนยัน คอมเมนต์ %d งาน", len(picks)), CancelLabel: "ไม่ต้อง",
		Plan: domain.WritePlan{Kind: domain.PlanComments, ProjectID: &projectID, Items: domain.MustJSON(targets), HTML: "<p>" + domain.EscapeHTML(message) + "</p>"},
	}, nil
}

func (a *App) recordsLink(projectID string) string { return a.DashboardURL(projectID) + "#records" }

func (a *App) executeFeature(ctx context.Context, plan domain.WritePlan) (Result, error) {
	switch plan.Kind {
	case domain.PlanRecordAdd:
		data := map[string]any{}
		_ = json.Unmarshal(plan.Data, &data)
		notionURL := ""
		if plan.NotionDoc != nil {
			if pg, err := a.Notion.CreatePage(ctx, a.Store.Prefs().NotionParentPageID, plan.NotionDoc.Title, plan.NotionDoc.Markdown); err == nil {
				notionURL = pg.URL
				data["notionUrl"] = notionURL
			} else {
				slog.Error("CR notion doc failed", "err", err)
			}
		}
		pid := deref(plan.ProjectID)
		if plan.ReplaceID != 0 {
			r, err := a.Records.Update(ctx, a.ws(), plan.ReplaceID, domain.RecordPatch{Data: data})
			if err != nil {
				return nil, err
			}
			code := ""
			if r != nil {
				code = r.CodeOr("")
			}
			return Result{"record": code, "links": []domain.Link{{Label: "แก้ " + code + " แล้ว", URL: a.recordsLink(pid)}}}, nil
		}
		var due *time.Time
		if plan.RecKind == domain.KindMilestone {
			var md domain.MilestoneData
			_ = json.Unmarshal(plan.Data, &md)
			t := domain.LocalToTime(md.Due, "17:00", a.Loc)
			due = &t
		}
		r, err := a.Records.Add(ctx, a.ws(), plan.RecKind, plan.ProjectID, data, plan.Status, due)
		if err != nil {
			return nil, err
		}
		label := r.TitleOrName()
		if label == "" {
			label = "บันทึกแล้ว"
		}
		label = strings.TrimSpace(r.CodeOr("") + " " + label)
		links := []domain.Link{{Label: label, URL: a.recordsLink(pid)}}
		if notionURL != "" {
			links = append(links, domain.Link{Label: "เอกสาร CR ใน Notion", URL: notionURL})
		}
		var rec any = r.ID
		if r.Code != nil {
			rec = *r.Code
		}
		return Result{"record": rec, "links": links}, nil

	case domain.PlanRecordUpdate:
		data := map[string]any{}
		_ = json.Unmarshal(plan.Data, &data)
		patch := domain.RecordPatch{Data: data}
		if plan.Status != "" {
			patch.Status = ptr(plan.Status)
		}
		r, err := a.Records.Update(ctx, a.ws(), plan.ID, patch)
		if err != nil {
			return nil, err
		}
		status, pid := "", ""
		if r != nil {
			status, pid = r.Status, deref(r.ProjectID)
		}
		return Result{"record": plan.Code, "status": status, "links": []domain.Link{{Label: plan.Code + " → " + domain.CrStatusTH(status), URL: a.recordsLink(pid)}}}, nil

	case domain.PlanEffort:
		total := 0.0
		for _, e := range plan.Entries {
			pid := e.ProjectID
			if _, err := a.Records.Add(ctx, a.ws(), domain.KindEffort, &pid, e.Data, "", nil); err != nil {
				return nil, err
			}
			total += e.Data.Hours
		}
		return Result{"logged": len(plan.Entries), "summary": "บันทึกเวลา " + num(total) + " ชม."}, nil

	case domain.PlanMeeting:
		ev, err := a.Google.CreateMeeting(ctx, port.MeetingRequest{Summary: plan.Summary, Description: plan.Description, StartISO: plan.StartISO, EndISO: plan.EndISO, Attendees: plan.Attendees})
		if err != nil {
			return nil, err
		}
		links := []domain.Link{}
		if ev.MeetURL != "" {
			links = append(links, domain.Link{Label: "Google Meet · " + strings.Replace(ev.MeetURL, "https://", "", 1), URL: ev.MeetURL})
		}
		if ev.HTMLLink != "" {
			links = append(links, domain.Link{Label: "เปิดใน Calendar", URL: ev.HTMLLink})
		}
		return Result{
			"meeting": map[string]any{"title": ev.Summary, "start": ev.Start, "meet": ev.MeetURL},
			"links":   links,
			"summary": fmt.Sprintf("นัด %s · ส่ง invite %d คน", ev.Summary, len(plan.Attendees)),
		}, nil

	case domain.PlanEmail:
		id, err := a.Google.SendEmail(ctx, port.Email{To: plan.To, Cc: plan.Cc, Subject: plan.Subject, HTML: plan.HTML})
		if err != nil {
			return nil, err
		}
		n := len(plan.To) + len(plan.Cc)
		if plan.NotionPageID != "" && a.Notion.Connected() {
			if err := a.Notion.Append(ctx, plan.NotionPageID, fmt.Sprintf("ส่ง minute ทางอีเมลแล้ว %s ถึง %d คน", a.today(), n)); err != nil {
				slog.Error("notion append failed", "err", err)
			}
		}
		return Result{"sent": id, "to": n, "summary": fmt.Sprintf(`ส่งอีเมล "%s" ถึง %d คน`, plan.Subject, n)}, nil

	case domain.PlanNotionPage:
		if plan.AppendTo != "" {
			if err := a.Notion.Append(ctx, plan.AppendTo, "# "+plan.Title+"\n"+plan.Markdown); err != nil {
				return nil, err
			}
			return Result{"notion": "appended", "summary": fmt.Sprintf(`เพิ่ม "%s" ใน Notion`, plan.Title)}, nil
		}
		pg, err := a.Notion.CreatePage(ctx, a.Store.Prefs().NotionParentPageID, plan.Title, plan.Markdown)
		if err != nil {
			return nil, err
		}
		return Result{"notion": pg.URL, "links": []domain.Link{{Label: "Notion · " + plan.Title, URL: pg.URL}}}, nil

	case domain.PlanComments:
		project, err := a.findProject(ctx, deref(plan.ProjectID))
		if err != nil {
			return nil, err
		}
		if project == nil {
			return nil, fmt.Errorf("project not found")
		}
		done := []string{}
		links := []domain.Link{}
		for _, it := range plan.Targets() {
			if err := a.tasks().AddComment(ctx, *project, it.ItemID, plan.HTML); err != nil {
				return nil, err
			}
			done = append(done, it.Key)
			links = append(links, domain.Link{Label: it.Key, URL: a.tasks().KeyURL(it.Key)})
		}
		return Result{"commented": strings.Join(done, ", "), "summary": fmt.Sprintf("ทวงอัปเดต %d งาน", len(done)), "links": links}, nil
	}
	return nil, fmt.Errorf("unknown plan %s", plan.Kind)
}
