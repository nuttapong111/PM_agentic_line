package usecase

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

// Cards for computed views (milestone, budget, capacity, meeting prep) and the proactive drafts.

func (a *App) milestoneCard(projectName string, m MilestoneStatus, buttons []domain.Btn) port.Message {
	missing := []DeliverableStatus{}
	for _, d := range m.Deliverables {
		if d.Status == "missing" {
			missing = append(missing, d)
		}
	}
	mp := 100
	if m.ModulePercent != nil {
		mp = *m.ModulePercent
	}
	tone := domain.ToneInfo
	if m.DaysLeft < 0 {
		tone = domain.ToneDanger
	} else if m.DaysLeft <= 14 && mp < 80 {
		tone = domain.ToneWarn
	}
	kicker := fmt.Sprintf("งวดงาน · อีก %d วัน", m.DaysLeft)
	if m.DaysLeft < 0 {
		kicker = "งวดงานเลยกำหนด"
	}
	dueRow := domain.CardRow{Label: "กำหนดส่งมอบ", Value: fmt.Sprintf("%s (อีก %d วัน)", domain.ThaiDate(m.Due, true), m.DaysLeft)}
	if m.DaysLeft < 7 {
		dueRow.Color = domain.ColorRed
	}
	rows := []domain.CardRow{dueRow}
	if m.ValuePercent != nil && *m.ValuePercent != 0 {
		rows = append(rows, domain.CardRow{Label: "มูลค่างวด", Value: num(*m.ValuePercent) + "% ของสัญญา"})
	}
	var bars []domain.CardBar
	if m.ModulePercent != nil {
		bars = []domain.CardBar{{Label: fmt.Sprintf("module ในงวดนี้ %d ตัว", len(m.Modules)), Percent: *m.ModulePercent}}
	}
	lines := []string{}
	for _, d := range m.Deliverables {
		if d.Status == "done" {
			continue
		}
		mark := "◐"
		if d.Status == "missing" {
			mark = "○"
		}
		tor := ""
		if d.Tor != "" {
			tor = " (TOR " + d.Tor + ")"
		}
		lines = append(lines, fmt.Sprintf("%s %s%s · %s", mark, d.Name, tor, d.Note))
	}
	if m.Done > 0 {
		lines = append(lines, fmt.Sprintf("✓ ครบแล้ว %d รายการ", m.Done))
	}
	sec := domain.CardSection{Title: fmt.Sprintf("สิ่งที่ต้องส่งตาม TOR %d/%d", m.Done, m.Total), Lines: lines}
	if len(missing) > 0 {
		sec.Tone = domain.ColorAmber
	}
	if buttons == nil {
		if len(missing) > 0 {
			names := make([]string, len(missing))
			for i, d := range missing {
				names[i] = d.Name
			}
			buttons = append(buttons, domain.Btn{
				Label:   fmt.Sprintf("สร้างงานที่ยังขาด %d งาน", len(missing)),
				Data:    cut("cmd:สร้างงานสำหรับรายการส่งมอบที่ยังไม่มีงานของ"+m.Name+": "+strings.Join(names, ", "), 300),
				Primary: true,
			})
		}
		buttons = append(buttons,
			domain.Btn{Label: "ดูงวดทั้งหมด", Data: "cmd:งวดงานทั้งหมดของโปรเจกต์นี้"},
			domain.Btn{Label: "ร่างเมลแจ้งลูกค้า", Data: cut("cmd:ร่างอีเมลแจ้งลูกค้าเรื่องความคืบหน้า"+m.Name, 300)},
		)
	}
	return a.View.Info(domain.InfoCard{
		Kicker: kicker, Title: m.Name + " · " + projectName, Tone: tone,
		Rows: rows, Bars: bars, Sections: []domain.CardSection{sec}, Buttons: buttons,
	})
}

func (a *App) budgetCard(b BudgetStatus) port.Message {
	over := b.BurnAheadPercent != nil && *b.BurnAheadPercent > 5
	tone := domain.ToneInfo
	if over {
		tone = domain.ToneWarn
	}
	sub := "ยังไม่ได้ตั้งงบ man-day"
	if b.HasBudget && b.TotalManDays != nil {
		sub = "สัญญา " + num(*b.TotalManDays) + " man-day"
	}
	var bars []domain.CardBar
	if b.TotalManDays != nil && *b.TotalManDays != 0 {
		used := 0
		if b.UsedPercent != nil {
			used = *b.UsedPercent
		}
		bar := domain.CardBar{Label: "ใช้ไปแล้ว", Percent: used, Value: num(b.UsedManDays) + " / " + num(*b.TotalManDays) + " md"}
		if over {
			bar.Tone = domain.ColorAmber
		}
		bars = append(bars, bar)
	} else {
		bars = append(bars, domain.CardBar{Label: "ใช้ไปแล้ว", Percent: 0, Value: num(b.UsedManDays) + " md"})
	}
	bars = append(bars, domain.CardBar{Label: "งานเสร็จแล้ว", Percent: b.ProgressPercent})
	note := ""
	switch {
	case b.BurnAheadPercent != nil && b.ProjectedOverrun != nil:
		if over {
			o := *b.ProjectedOverrun
			if o < 0 {
				o = 0
			}
			note = fmt.Sprintf("ใช้แรงงานเร็วกว่าความคืบหน้า %d%% ถ้าเป็นแบบนี้ต่อ คาดว่าจะเกินงบ %s man-day", *b.BurnAheadPercent, num(o))
		} else {
			note = "การใช้แรงงานสอดคล้องกับความคืบหน้า"
		}
	case b.Entries == 0:
		note = "ยังไม่มีการบันทึกเวลา พิมพ์เช่น \"วันนี้ต้นทำ FDABIGDATA-98 4 ชม.\""
	}
	mods := []string{}
	for _, x := range b.Modules {
		s := x.Name + " · " + num(x.Used)
		if x.Budget != nil && *x.Budget != 0 {
			s += " / " + num(*x.Budget)
		}
		mods = append(mods, s+" md")
	}
	return a.View.Info(domain.InfoCard{
		Kicker: "งบแรงงาน · ข้อมูลถึง " + domain.ThaiDate(a.today(), false), Title: b.Project, Tone: tone,
		Subtitle: sub, Bars: bars, Note: note,
		Sections: []domain.CardSection{
			{Title: "ราย module", Lines: mods},
			{Title: "ที่มา", Tone: domain.ColorMuted, Lines: []string{b.Basis}},
		},
		Buttons: []domain.Btn{{Label: "ดูใน Dashboard", URL: a.DashboardURL("") + "#records"}},
	})
}

func (a *App) capacityCard(c Capacity) port.Message {
	title, tone := "ไม่มีใครงานล้น", domain.ToneOK
	if len(c.Overloaded) > 0 {
		title, tone = fmt.Sprintf("มี %d คนงานล้น", len(c.Overloaded)), domain.ToneWarn
	}
	bars := []domain.CardBar{}
	for i, p := range c.People {
		if i == 8 {
			break
		}
		label := p.Name
		if len(p.Leave) > 0 {
			ds := make([]string, len(p.Leave))
			for j, d := range p.Leave {
				ds[j] = domain.ThaiDate(d, false)
			}
			label += " · ลา " + strings.Join(ds, ", ")
		}
		t := domain.ColorGreen
		if p.LoadPercent > 100 {
			t = domain.ColorRed
		} else if p.LoadPercent > 85 {
			t = domain.ColorAmber
		}
		bars = append(bars, domain.CardBar{Label: label, Percent: min(100, p.LoadPercent), Value: strconv.Itoa(p.LoadPercent) + "%", Tone: t})
	}
	var buttons []domain.Btn
	if len(c.Overloaded) > 0 {
		buttons = []domain.Btn{{
			Label: "แนะนำการย้ายงาน", Primary: true,
			Data: cut("cmd:แนะนำย้ายงานของ "+strings.Join(c.Overloaded, ", ")+" ให้คนที่งานน้อยกว่า", 300),
		}}
	}
	return a.View.Info(domain.InfoCard{
		Kicker: "กำลังคน · " + c.WeekLabel, Title: title, Tone: tone,
		Subtitle: c.Project + " · " + c.LeaveSource, Bars: bars, Note: c.Basis, Buttons: buttons,
	})
}

func (a *App) prepCard(p MeetingPrep) port.Message {
	ev := p.Event
	start, end := ev.StartTime(), ev.EndTime()
	mins := round(float64(start.Sub(a.Now()).Milliseconds()) / 60000)
	kicker := "สรุปก่อนประชุม"
	if mins > 0 && mins < 120 {
		kicker = fmt.Sprintf("ประชุมในอีก %d นาที", mins)
	}
	when := domain.HM(start, a.Loc) + "–" + domain.HM(end, a.Loc)
	if ev.MeetURL != "" {
		when += " · Google Meet"
	}
	sub := ""
	if p.Project != nil {
		sub = *p.Project
	}
	progress := []string{}
	if p.Progress != nil {
		progress = []string{*p.Progress}
	}
	actions := append([]string{}, p.OpenActions...)
	for _, d := range p.DoneActions {
		actions = append(actions, "✓ "+d)
	}
	actSec := domain.CardSection{Title: fmt.Sprintf("Action items จากครั้งก่อน · ค้าง %d", len(p.OpenActions)), Lines: actions}
	if len(p.OpenActions) > 0 {
		actSec.Tone = domain.ColorAmber
	}
	var buttons []domain.Btn
	if ev.MeetURL != "" {
		buttons = append(buttons, domain.Btn{Label: "เปิด Google Meet", URL: ev.MeetURL, Primary: true})
	}
	if p.NotionURL != "" {
		buttons = append(buttons, domain.Btn{Label: "เปิดบันทึกครั้งก่อน", URL: p.NotionURL})
	}
	return a.View.Info(domain.InfoCard{
		Kicker: kicker, Title: ev.Summary, Subtitle: sub,
		Rows: []domain.CardRow{
			{Label: "เวลา", Value: when},
			{Label: "ผู้เข้าร่วม", Value: fmt.Sprintf("%d คน", len(ev.Attendees))},
		},
		Sections: []domain.CardSection{
			{Title: "ความคืบหน้า", Lines: progress},
			actSec,
			{Title: "ควรหยิบมาคุย", Lines: append(append([]string{}, p.Risks...), p.Crs...)},
		},
		Buttons: buttons,
	})
}

// addProactive saves a draft so the card's buttons can send it later.
func (a *App) addProactive(name string, pv domain.Preview) string {
	now := a.nowMs()
	id := strconv.FormatInt(now, 36) + randomHex(2)
	a.Store.Update(func(s *domain.AppState) {
		if s.Proactive == nil {
			s.Proactive = map[string]*domain.Proactive{}
		}
		s.Proactive[id] = &domain.Proactive{ID: id, CreatedAt: now, Name: name, Preview: pv}
	})
	return id
}

// WeeklyReportProactive turns a weekly report into a draft card (send button when Google and recipients are set).
func (a *App) WeeklyReportProactive(r WeeklyReport) (string, port.Message) {
	w := a.Store.Prefs().Weekly
	canSend := a.Google.Connected() && len(w.To) > 0
	sub := "ยังไม่ได้ตั้งผู้รับ หรือยังไม่ได้เชื่อม Google"
	if canSend {
		sub = "ถึง " + strings.Join(w.To, ", ")
		if len(w.Cc) > 0 {
			sub += fmt.Sprintf(" · สำเนา %d", len(w.Cc))
		}
	}
	lines := []domain.PreviewLine{}
	for _, p := range r.Projects {
		s := ""
		if p.Percent != nil {
			s = p.summary() + " · "
		}
		lines = append(lines, domain.PreviewLine{Text: p.Name, Sub: s + p.Note, Warn: p.Overdue > 0})
	}
	var dn []string
	for i, d := range r.Done {
		if i == 2 {
			break
		}
		dn = append(dn, d.Key+" "+d.Name)
	}
	doneSub := strings.Join(dn, " · ")
	if len(r.Done) > 2 {
		doneSub += fmt.Sprintf(" + อีก %d", len(r.Done)-2)
	}
	if doneSub == "" {
		doneSub = "-"
	}
	lines = append(lines, domain.PreviewLine{Text: fmt.Sprintf("เสร็จสัปดาห์นี้ %d งาน", len(r.Done)), Sub: doneSub})
	if len(r.Next) > 0 {
		lines = append(lines, domain.PreviewLine{Text: "สัปดาห์หน้า", Sub: strings.Join(r.Next[:min(3, len(r.Next))], " · ")})
	}
	if len(r.Risks) > 0 {
		lines = append(lines, domain.PreviewLine{Text: "ความเสี่ยงที่ต้องการความช่วยเหลือ", Sub: strings.Join(r.Risks[:min(2, len(r.Risks))], " · "), Warn: true})
	}
	warnings := []string{}
	confirm := "ส่งอีเมล"
	if !canSend {
		warnings = []string{"ตั้งผู้รับรายงานและเชื่อม Google ได้ที่หน้าตั้งค่า ระหว่างนี้กดดูฉบับเต็มแล้วคัดลอกไปส่งเองได้"}
		confirm = "ตั้งค่าผู้รับ"
	}
	pv := domain.Preview{
		Kicker: "ร่างรายงานประจำสัปดาห์ · รอตรวจ", Title: r.Subject, Subtitle: sub,
		Lines: lines, Warnings: warnings, ConfirmLabel: confirm, FullHTML: r.HTML,
		Plan: domain.WritePlan{Kind: domain.PlanEmail, To: w.To, Cc: w.Cc, Subject: r.Subject, HTML: r.HTML},
	}
	id := a.addProactive("weekly_report", pv)
	if canSend {
		return id, a.View.Confirm(pv, id, true, a.PreviewURL(id))
	}
	summary := make([]string, len(pv.Lines))
	for i, l := range pv.Lines {
		summary[i] = l.Text + ": " + l.Sub
	}
	return id, a.View.Info(domain.InfoCard{
		Kicker: pv.Kicker, Title: r.Subject, Subtitle: pv.Subtitle,
		Sections: []domain.CardSection{{Title: "สรุป", Lines: summary}},
		Buttons: []domain.Btn{
			{Label: "ดูฉบับเต็ม", URL: a.PreviewURL(id), Primary: true},
			{Label: "ตั้งค่าผู้รับ", URL: a.SettingsURL("notify")},
		},
	})
}

// RetroProactive turns a retro into a draft card (save-to-Notion button when Notion is set up).
func (a *App) RetroProactive(r Retro) port.Message {
	lines := []domain.PreviewLine{{Text: "ผลลัพธ์", Sub: fmt.Sprintf("เสร็จ %d / %d งาน (%d%%) · scope เพิ่ม +%d (%d%%)", r.Done, r.Total, r.Percent, r.AddedMidway, r.ScopePercent)}}
	if len(r.Good) > 0 {
		lines = append(lines, domain.PreviewLine{Text: "ที่ไปได้ดี", Sub: strings.Join(r.Good, " · ")})
	}
	if len(r.Improve) > 0 {
		lines = append(lines, domain.PreviewLine{Text: "ที่ต้องปรับ", Sub: strings.Join(r.Improve, " · "), Warn: true})
	}
	lines = append(lines, domain.PreviewLine{Text: "หัวข้อชวนคุย", Sub: strings.Join(r.Topics, " · ")})
	kicker := r.Cycle + " จบแล้ว · ร่างสรุป retrospective"
	if a.Notion.Connected() && a.Store.Prefs().NotionParentPageID != "" {
		pv := domain.Preview{
			Kicker: kicker, Title: "Retro · " + r.Cycle,
			Subtitle: r.Project + " · " + domain.ThaiDate(r.Start, false) + " – " + domain.ThaiDate(r.End, false),
			Lines:    lines, Warnings: []string{}, ConfirmLabel: "บันทึกลง Notion",
			Plan: domain.WritePlan{Kind: domain.PlanNotionPage, Title: "Retro · " + r.Cycle + " · " + r.Project, Markdown: r.Markdown},
		}
		id := a.addProactive("retro", pv)
		return a.View.Confirm(pv, id, true, "")
	}
	secs := make([]domain.CardSection, len(lines))
	for i, l := range lines {
		secs[i] = domain.CardSection{Title: l.Text, Lines: []string{l.Sub}}
		if l.Warn {
			secs[i].Tone = domain.ColorAmber
		}
	}
	return a.View.Info(domain.InfoCard{
		Kicker: kicker, Title: "Retro · " + r.Cycle, Subtitle: r.Project, Sections: secs,
		Buttons: []domain.Btn{{Label: "เชื่อม Notion เพื่อบันทึก", URL: a.SettingsURL("")}},
	})
}
