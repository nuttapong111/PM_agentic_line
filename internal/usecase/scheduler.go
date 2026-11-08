package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

// Everything the bot sends on its own, checked once a minute against the times in Settings:
// morning digest, reminders, milestone / contract-deadline alerts, meeting prep, minutes nudge,
// weekly nudges for stale work, weekly report draft, retro when a sprint ends.
// Each alert has a dedupe key in state.sent so a restart never sends it twice.

// Push sends messages to the owner (at most 5 per call).
type Push func(ctx context.Context, msgs []port.Message) error

var blockRe = regexp.MustCompile(`(?i)block`)

// Minute runs the jobs due this minute. Call it once a minute; overlapping calls are skipped.
func (a *App) Minute(ctx context.Context, push Push) {
	if a.Cfg.OwnerUserID != "" {
		if a.tickMu.TryLock() {
			go func() {
				defer a.tickMu.Unlock()
				a.tick(ctx, push)
			}()
		}
	}
	now := domain.HM(a.Now(), a.Loc)
	if mm, _ := strconv.Atoi(now[3:]); mm%dashEvery == 0 {
		go a.RefreshDashboards(ctx)
	}
	if now == "23:50" {
		go a.SnapshotAll(ctx)
	}
}

func (a *App) workday() bool { d := domain.Weekday(a.today()); return d >= 1 && d <= 5 }

func (a *App) tick(ctx context.Context, push Push) {
	p := a.Store.Prefs()
	now := domain.HM(a.Now(), a.Loc)
	today := a.today()
	wd := domain.Weekday(today)
	safe := func(name string, fn func() error) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("job panicked", "job", name, "panic", r)
			}
		}()
		if err := fn(); err != nil {
			slog.Error("job failed", "job", name, "err", err)
		}
	}

	// Reminders the owner set: always delivered, even in quiet hours.
	safe("reminders", func() error { return a.fireReminders(ctx, push) })

	if now == p.DigestTime && (!p.WorkdaysOnly || a.workday()) && a.Store.Once("digest:"+today) {
		safe("digest", func() error { return push(ctx, []port.Message{a.BuildDigest(ctx)}) })
	}
	if domain.InQuietHours(p.QuietFrom, p.QuietTo, now) {
		return
	}
	// 08:31 on workdays: milestone + contract deadline alerts (after the digest).
	if now == "08:31" && a.workday() {
		safe("deadlines", func() error { return a.deadlineAlerts(ctx, push, today) })
	}
	if p.MeetingPrepMinutes > 0 && a.Google.Connected() {
		safe("meeting prep", func() error { return a.prepAlerts(ctx, push, p.MeetingPrepMinutes) })
	}
	if mm, _ := strconv.Atoi(now[3:]); a.Google.Connected() && a.Notion.Connected() && mm%15 == 0 {
		safe("minutes", func() error { return a.minutesNudge(ctx, push) })
	}
	if p.Nudge.Enabled && wd == p.Nudge.Day && now == p.Nudge.Time && a.Store.Once("nudge:"+today) {
		safe("nudge", func() error { return a.nudges(ctx, push) })
	}
	if p.Weekly.Enabled && wd == p.Weekly.Day && now == p.Weekly.Time && a.Store.Once("weekly:"+today) {
		safe("weekly", func() error {
			r, err := a.WeeklyReport(ctx)
			if err != nil {
				return err
			}
			_, msg := a.WeeklyReportProactive(r)
			return push(ctx, []port.Message{msg})
		})
	}
	if now == "15:00" {
		safe("retro", func() error { return a.retroAlerts(ctx, push, today) })
	}
}

func (a *App) fireReminders(ctx context.Context, push Push) error {
	due, err := a.Records.DueReminders(ctx)
	if err != nil {
		return err
	}
	tp := a.tasks()
	for _, r := range due {
		sent := "sent"
		if _, err := a.Records.Update(ctx, r.WS, r.ID, domain.RecordPatch{Status: &sent}); err != nil {
			return err
		}
		var d domain.ReminderData
		r.Decode(&d)
		rows := []domain.CardRow{}
		url := ""
		if d.ItemKey != "" {
			if row, u, err := a.reminderItemRows(ctx, tp, d.ItemKey); err == nil {
				rows, url = row, u
			} else {
				rows = append(rows, domain.CardRow{Label: "งาน", Value: d.ItemKey})
			}
		}
		buttons := []domain.Btn{}
		if url != "" {
			buttons = append(buttons, domain.Btn{Label: "ดูงาน", URL: url, Primary: true})
		}
		if d.ItemKey != "" {
			buttons = append(buttons, domain.Btn{Label: "คอมเมนต์", Data: "cmd:คอมเมนต์ใน " + d.ItemKey + " ว่า ขออัปเดตความคืบหน้าหน่อยครับ"})
		}
		id := strconv.FormatInt(r.ID, 10)
		buttons = append(buttons, domain.Btn{Label: "เลื่อน 1 วัน", Data: "snooze:" + id}, domain.Btn{Label: "เสร็จแล้ว", Data: "rdone:" + id})
		if err := push(ctx, []port.Message{a.View.Info(domain.InfoCard{
			Kicker: "เตือนความจำ · " + domain.ThaiDate(a.today(), true) + " " + domain.HM(a.Now(), a.Loc),
			Title:  d.Text, Tone: domain.ToneWarn, Rows: rows, Buttons: buttons,
		})}); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) reminderItemRows(ctx context.Context, tp port.TaskProvider, key string) ([]domain.CardRow, string, error) {
	it, err := tp.WorkItemByKey(ctx, key)
	if err != nil {
		return nil, "", err
	}
	states, err := tp.States(ctx, it.ProjectID)
	if err != nil {
		return nil, "", err
	}
	members, err := tp.Members(ctx, it.ProjectID)
	if err != nil {
		return nil, "", err
	}
	st := "?"
	for _, s := range states {
		if s.ID == it.StateID {
			st = s.Name
		}
	}
	var who []string
	for _, id := range it.AssigneeIDs {
		for _, m := range members {
			if m.ID == id && m.DisplayName != "" {
				who = append(who, m.DisplayName)
			}
		}
	}
	whoS := strings.Join(who, ", ")
	if whoS == "" {
		whoS = "ไม่มีเจ้าของ"
	}
	since := ""
	if up, ok := domain.ParseInstant(it.UpdatedAt); ok {
		if days := int(a.Now().Sub(up).Hours() / 24); days != 0 {
			since = fmt.Sprintf(" มา %d วัน", days)
		}
	}
	now := domain.CardRow{Label: "ตอนนี้", Value: st + since + " · " + whoS}
	if blockRe.MatchString(st) {
		now.Color = domain.ColorRed
	}
	return []domain.CardRow{{Label: "งาน", Value: it.Key + " " + it.Name}, now}, tp.ItemURL(it), nil
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (a *App) deadlineAlerts(ctx context.Context, push Push, today string) error {
	alertDays := a.Store.Prefs().MilestoneAlertDays
	ps, err := a.tasks().Projects(ctx)
	if err != nil {
		return err
	}
	out := []port.Message{}
	for _, project := range ps {
		// payment milestones
		ms, _ := a.MilestonesFor(ctx, project, false)
		for _, m := range ms {
			if (containsInt(alertDays, m.DaysLeft) || m.DaysLeft == 1) && a.Store.Once(fmt.Sprintf("ms:%d:%d", m.ID, m.DaysLeft)) {
				out = append(out, a.milestoneCard(project.Name, m, nil))
			}
		}
		// modules whose contract date is close and progress is behind
		metrics, err := a.ProjectMetrics(ctx, project)
		if err != nil {
			continue
		}
		_ = a.Snapshot(ctx, metrics)
		for _, mod := range metrics.Modules {
			if mod.Target == nil || mod.Status == domain.ModDone || mod.Status == domain.ModOnTrack {
				continue
			}
			left := domain.DaysBetween(today, *mod.Target)
			if left < 0 || left > 7 {
				continue
			}
			bucket := 7
			if left <= 3 {
				bucket = 3
			}
			if !a.Store.Once(fmt.Sprintf("mod:%s:%s:%s:%d", project.ID, mod.Name, *mod.Target, bucket)) {
				continue
			}
			note := fmt.Sprintf("เหลือ %d งาน", mod.Total-mod.Done)
			if metrics.Counts.Blocked > 0 {
				note += fmt.Sprintf(" · Blocked ในโปรเจกต์ %d", metrics.Counts.Blocked)
			}
			out = append(out, a.View.Info(domain.InfoCard{
				Kicker: "เตือนกำหนดส่งตามสัญญา", Title: "Module เสี่ยงไม่ทัน", Tone: domain.ToneDanger,
				Rows: []domain.CardRow{
					{Label: "Module", Value: mod.Name + " (" + project.Name + ")"},
					{Label: "กำหนดส่ง", Value: fmt.Sprintf("%s (อีก %d วัน)", domain.ThaiDate(*mod.Target, true), left), Color: domain.ColorRed},
				},
				Bars: []domain.CardBar{{Label: "ความคืบหน้า", Percent: mod.Percent, Tone: domain.ColorRed}},
				Note: note,
				Buttons: []domain.Btn{
					{Label: "ดูงานที่เหลือ", Data: cut("cmd:งานที่ยังไม่เสร็จใน module "+mod.Name+" ของ "+project.Name, 300), Primary: true},
					{Label: "ร่างเมลแจ้งลูกค้า", Data: cut("cmd:ร่างอีเมลแจ้งลูกค้าว่า module "+mod.Name+" อาจส่งไม่ทัน "+domain.ThaiDate(*mod.Target, true), 300)},
				},
			}))
		}
	}
	for i := 0; i < len(out); i += 5 {
		if err := push(ctx, out[i:min(i+5, len(out))]); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) prepAlerts(ctx context.Context, push Push, minutes int) error {
	now := a.Now()
	from := now.Add(time.Duration(minutes-1) * time.Minute)
	to := now.Add(time.Duration(minutes+1) * time.Minute)
	evs, err := a.Google.ListEvents(ctx, from, to.Add(time.Hour), "primary", "")
	if err != nil {
		return err
	}
	for _, e := range evs {
		if e.AllDay || len(e.Attendees) <= 1 {
			continue
		}
		start := e.StartTime()
		if start.Before(from) || !start.Before(to) {
			continue
		}
		if !a.Store.Once("prep:" + e.ID + ":" + e.Start) {
			continue
		}
		prep, err := a.MeetingPrep(ctx, e)
		if err != nil {
			return err
		}
		if err := push(ctx, []port.Message{a.prepCard(prep)}); err != nil {
			return err
		}
	}
	return nil
}

// minutesNudge: a meeting with attendees ended 30–90 minutes ago and Notion has a page about it → offer the minute.
func (a *App) minutesNudge(ctx context.Context, push Push) error {
	now := a.Now()
	evs, err := a.Google.ListEvents(ctx, now.Add(-3*time.Hour), now, "primary", "")
	if err != nil {
		return err
	}
	for _, e := range evs {
		if e.AllDay || len(e.Attendees) < 2 {
			continue
		}
		ended := now.Sub(e.EndTime())
		if ended < 30*time.Minute || ended > 90*time.Minute {
			continue
		}
		seen := false
		a.Store.Read(func(s *domain.AppState) { _, seen = s.Sent["minutes:"+e.ID] })
		if seen {
			continue
		}
		pages, err := a.Notion.Search(ctx, e.Summary, 3)
		if err != nil {
			return err
		}
		var hit *domain.NotionPage
		for i, pg := range pages {
			if ed, ok := domain.ParseInstant(pg.EditedAt); ok && !ed.Before(e.StartTime().Add(-time.Hour)) {
				hit = &pages[i]
				break
			}
		}
		if hit == nil {
			continue
		}
		a.Store.Once("minutes:" + e.ID)
		msg := a.View.QuickText(fmt.Sprintf("ประชุม \"%s\" จบแล้ว และพบจดประชุมใน Notion \"%s\"\nให้ร่างอีเมล minute ถึงผู้เข้าประชุม %d คนไหมครับ", e.Summary, hit.Title, len(e.Attendees)), []domain.QuickItem{
			{Label: "ร่าง minute", Data: cut(fmt.Sprintf("cmd:ร่างอีเมล minute ของประชุม \"%s\" วันนี้ จากหน้า Notion id %s ส่งถึงผู้เข้าประชุมทุกคน", e.Summary, hit.ID), 300), DisplayText: "ร่าง minute"},
			{Label: "ไม่ต้อง", Data: "cmd:ไม่ต้องส่ง minute ครับ", DisplayText: "ไม่ต้อง"},
		})
		if err := push(ctx, []port.Message{msg}); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) nudges(ctx context.Context, push Push) error {
	ps, err := a.tasks().Projects(ctx)
	if err != nil {
		return err
	}
	for _, p := range ps {
		pv, err := a.NudgePreview(ctx, p.ID, nil, "")
		if err != nil {
			continue // nothing stale
		}
		id := a.addProactive("nudge_stale", pv)
		if err := push(ctx, []port.Message{a.View.Confirm(pv, id, true, "")}); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) retroAlerts(ctx context.Context, push Push, today string) error {
	ps, err := a.tasks().Projects(ctx)
	if err != nil {
		return err
	}
	for _, p := range ps {
		cycles, _ := a.tasks().Cycles(ctx, p.ID)
		for i := range cycles {
			c := cycles[i]
			if c.EndDate == nil || !strings.HasPrefix(*c.EndDate, today) || !a.Store.Once("retro:"+c.ID) {
				continue
			}
			r, err := a.Retro(ctx, p, &c)
			if err != nil {
				return err
			}
			if r == nil {
				continue
			}
			if err := push(ctx, []port.Message{a.RetroProactive(*r)}); err != nil {
				return err
			}
			a.logActivity(ctx, "retro", "ร่าง retro "+c.Name, &p.ID, nil)
		}
	}
	return nil
}
