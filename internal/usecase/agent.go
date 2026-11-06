package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/config"
	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

// The agent loop: Claude + tools, with a pause for the owner's confirmation before any write.

const (
	idleReset = 30 * time.Minute
	// imageFollowUp: a photo sent within this time of the last message follows that message's request.
	imageFollowUp = 5 * time.Minute
	maxHistory    = 60
	maxReplyMsg   = 5 // LINE allows at most 5 messages per reply
)

var (
	resetRe    = regexp.MustCompile(`(?i)^(เริ่มใหม่|reset|ล้างแชท)$`)
	settingsRe = regexp.MustCompile(`(?i)^(ตั้งค่า|setting|settings)$`)
)

// claudeKey is the key saved on the Settings page, else ANTHROPIC_API_KEY. "" = none.
func (a *App) claudeKey() string {
	if c := a.Store.Connections().Claude; c != nil && c.KeyEnc != "" {
		if k, ok := a.Secrets.Decrypt(c.KeyEnc); ok && k != "" {
			return k
		}
	}
	if config.IsSet(a.Cfg.AnthropicAPIKey) {
		return a.Cfg.AnthropicAPIKey
	}
	return ""
}

func (a *App) dynamicContext(current string) string {
	today := a.today()
	if current == "" {
		current = "ยังไม่ได้เลือก"
	}
	return fmt.Sprintf("วันนี้: %s (%s) เขตเวลา %s\nโปรเจกต์ปัจจุบัน: %s", today, domain.ThaiDate(today, true), a.Cfg.Timezone, current)
}

func (a *App) text(t string) []port.Message { return []port.Message{a.View.Text(t)} }

// ---------------------------------------------------------------- public API

// HandleText answers a text message from the owner.
func (a *App) HandleText(ctx context.Context, userText string) []port.Message {
	t := strings.TrimSpace(userText)
	if resetRe.MatchString(t) {
		a.Store.Update(func(s *domain.AppState) { s.ResetConversation() })
		return a.text("เริ่มบทสนทนาใหม่แล้วครับ")
	}
	if settingsRe.MatchString(t) {
		return a.text("หน้าตั้งค่า (เปิดบนคอมสะดวกกว่า)\n" + a.SettingsURL("") + "\n\nลิงก์นี้ใช้แทนรหัสผ่าน อย่าส่งต่อนะครับ")
	}
	now := a.nowMs()
	a.Store.Update(func(s *domain.AppState) {
		// Start fresh after 30 idle minutes or when history is long.
		if now-s.Conversation.LastAt > idleReset.Milliseconds() || len(s.Conversation.Messages) > maxHistory {
			s.ResetConversation()
		}
	})
	// A new message while a card is pending = the card was not confirmed.
	a.closePending("ผู้ใช้ไม่ได้กดยืนยัน และพิมพ์ข้อความใหม่แทน ให้ถือว่ายังไม่ได้ทำรายการนี้")
	a.appendUser(domain.TextBlock(userText))
	return a.runLoop(ctx)
}

// HandleImage reads a photo (whiteboard, notebook, screenshot), usually into a create_work_items card.
func (a *App) HandleImage(ctx context.Context, base64Data, mediaType, caption string) []port.Message {
	now := a.nowMs()
	recent := false
	a.Store.Update(func(s *domain.AppState) {
		if now-s.Conversation.LastAt > idleReset.Milliseconds() {
			s.ResetConversation()
		}
		recent = len(s.Conversation.Messages) > 0 && now-s.Conversation.LastAt <= imageFollowUp.Milliseconds()
	})
	a.closePending("ผู้ใช้ส่งรูปใหม่แทนการกดยืนยัน ให้ถือว่ายังไม่ได้ทำรายการนี้")
	if caption == "" && recent {
		// LINE images carry no caption: the instruction is usually the text sent just before.
		caption = "นี่คือรูปที่ผู้ใช้ส่งมาต่อจากข้อความก่อนหน้า ให้ทำตามที่ผู้ใช้ขอไว้ในข้อความล่าสุดโดยใช้ข้อมูลในรูป (เช่น สร้างงวดงาน บันทึกความเสี่ยง หรือ CR) ถ้าข้อความก่อนหน้าไม่ได้ขออะไรเกี่ยวกับรูป ให้แปลงรายการในรูปเป็นงานในโปรเจกต์ปัจจุบัน (ใส่ใน cycle ปัจจุบัน)"
	}
	if caption == "" {
		caption = "แปลงรายการในรูปนี้เป็นงานในโปรเจกต์ปัจจุบันให้หน่อย (ใส่ใน cycle ปัจจุบัน) ถ้าในรูปไม่ใช่รายการงาน ให้บอกสั้น ๆ ว่าเห็นอะไร"
	}
	a.appendUser(
		domain.Block{"type": "image", "source": map[string]any{"type": "base64", "media_type": mediaType, "data": base64Data}},
		domain.TextBlock(caption),
	)
	defer a.Store.Update(func(s *domain.AppState) {
		// Images are large: keep only a placeholder in the saved history.
		for i := range s.Conversation.Messages {
			for j, b := range s.Conversation.Messages[i].Content {
				if b["type"] == "image" {
					s.Conversation.Messages[i].Content[j] = domain.TextBlock("[รูปภาพที่ผู้ใช้ส่งมา]")
				}
			}
		}
	})
	return a.runLoop(ctx)
}

// HandleMenu answers a postback, including the rich menu commands.
func (a *App) HandleMenu(ctx context.Context, data string) []port.Message {
	switch data {
	case "cmd:digest":
		return []port.Message{a.BuildDigest(ctx)}
	case "cmd:switch":
		ps, err := a.tasks().Projects(ctx)
		if err != nil {
			return a.text(a.errText(err))
		}
		return []port.Message{a.View.ProjectPicker("เลือกโปรเจกต์ปัจจุบันครับ", ps)}
	case "cmd:settings":
		return a.text("หน้าตั้งค่า\n" + a.SettingsURL(""))
	case "cmd:create":
		return a.text("พิมพ์งานที่จะสร้างได้เลยครับ เช่น\nสร้างงานหน้า login ด้วย OTP แตกเป็น FE/BE/QA ให้ต้นทำ BE ส่งศุกร์หน้า")
	}
	return a.HandlePostback(ctx, data)
}

// Welcome is the reply to a new follower.
func (a *App) Welcome(ctx context.Context) []port.Message {
	msgs := a.text("สวัสดีครับ ผมคือ PM Assistant ถามสถานะงาน สั่งสร้างหรือแก้งานได้ด้วยข้อความ ส่งรูปไวท์บอร์ดให้แปลงเป็นงานก็ได้ หรือใช้เมนูด้านล่าง\n\nเชื่อมเครื่องมือต่าง ๆ ได้ที่หน้าตั้งค่า พิมพ์ \"ตั้งค่า\" เพื่อรับลิงก์")
	ps, _ := a.tasks().Projects(ctx)
	if len(ps) > 1 && a.Store.CurrentProject() == "" {
		msgs = append(msgs, a.View.ProjectPicker("เริ่มจากเลือกโปรเจกต์ปัจจุบันก่อนครับ", ps))
	}
	return msgs
}

// HandlePostback answers a card button.
func (a *App) HandlePostback(ctx context.Context, data string) []port.Message {
	kind, arg, _ := strings.Cut(data, ":")
	switch kind {
	case "project":
		ps, _ := a.tasks().Projects(ctx)
		var p *domain.Project
		for i := range ps {
			if ps[i].ID == arg {
				p = &ps[i]
			}
		}
		if p == nil {
			return a.text("ไม่พบโปรเจกต์นี้แล้วครับ")
		}
		lastUser := false
		a.Store.Update(func(s *domain.AppState) {
			s.CurrentProjectID = &p.ID
			m := s.Conversation.Messages
			lastUser = len(m) > 0 && m[len(m)-1].Role == "user"
		})
		if lastUser {
			a.appendUser(domain.TextBlock("เลือกโปรเจกต์ " + p.Name))
			return a.runLoop(ctx)
		}
		return a.text("ตั้ง " + p.Name + " เป็นโปรเจกต์ปัจจุบันแล้วครับ")
	case "cmd":
		m := map[string]string{
			"overdue": "งานไหนเลยกำหนดบ้าง",
			"mine":    "งานของฉันที่ยังเปิดอยู่มีอะไรบ้าง",
			"status":  "สถานะโปรเจกต์ปัจจุบันเป็นยังไง",
		}
		if v, ok := m[arg]; ok {
			arg = v
		}
		return a.HandleText(ctx, arg)
	case "pconfirm", "pcancel", "pedit":
		return a.handleProactive(ctx, kind, arg)
	case "rcancel", "snooze", "rdone":
		id, _ := strconv.ParseInt(arg, 10, 64)
		return a.handleReminderButton(ctx, kind, id)
	}
	var pending *domain.PendingAction
	a.Store.Read(func(s *domain.AppState) {
		if s.Pending != nil {
			c := *s.Pending
			pending = &c
		}
	})
	if pending == nil || pending.ID != arg {
		return a.text("การ์ดนี้หมดอายุแล้วครับ ลองสั่งใหม่อีกครั้งได้เลย")
	}
	switch kind {
	case "cancel":
		a.closePending("ผู้ใช้กดยกเลิก ไม่ได้ทำรายการนี้")
		return a.text("ยกเลิกแล้วครับ")
	case "edit":
		a.closePending("ผู้ใช้กดแก้ไข ยังไม่ได้ทำรายการนี้ รอผู้ใช้บอกว่าจะแก้อะไร แล้วเรียกเครื่องมือใหม่")
		return a.text("จะแก้ตรงไหนครับ พิมพ์บอกได้เลย เช่น เปลี่ยนคนทำ QA เป็นมิ้น")
	case "confirm":
		return a.confirmPending(ctx, *pending)
	}
	return a.text("ไม่เข้าใจคำสั่งนี้ครับ")
}

func (a *App) handleProactive(ctx context.Context, kind, id string) []port.Message {
	var pa *domain.Proactive
	a.Store.Read(func(s *domain.AppState) {
		if p := s.Proactive[id]; p != nil {
			c := *p
			pa = &c
		}
	})
	if pa == nil {
		return a.text("การ์ดนี้หมดอายุหรือทำไปแล้วครับ")
	}
	pv := pa.Preview
	drop := func() { a.Store.Update(func(s *domain.AppState) { delete(s.Proactive, id) }) }
	switch kind {
	case "pcancel":
		drop()
		return a.text("รับทราบครับ ไม่ทำรายการนี้")
	case "pedit":
		drop()
		parts := []string{pv.Title, pv.Subtitle}
		for _, l := range pv.Lines {
			parts = append(parts, l.Text+": "+l.Sub)
		}
		summary := cut(strings.Join(parts, "\n"), 3000)
		to := ""
		if pv.Plan.Kind == domain.PlanEmail {
			to = "ผู้รับ: " + strings.Join(pv.Plan.To, ", ")
		}
		a.appendUser(domain.TextBlock(fmt.Sprintf("(ร่างที่บอทเตรียมไว้ ชนิด %s:\n%s\n%s)\nผู้ใช้กดแก้ไขร่างนี้ ให้รอผู้ใช้บอกว่าจะแก้อะไร แล้วเรียกเครื่องมือที่เหมาะสมใหม่", pa.Name, summary, to)))
		return a.text("จะแก้ตรงไหนครับ พิมพ์บอกได้เลย")
	}
	if pv.Plan.Kind == domain.PlanEmail && len(pv.Plan.To) == 0 {
		return a.text("ยังไม่ได้ตั้งผู้รับครับ ตั้งได้ที่\n" + a.SettingsURL("notify") + "\nหรือพิมพ์บอกว่าจะส่งถึงใคร")
	}
	drop()
	r, err := a.ExecuteWrite(ctx, pv.Plan)
	if err != nil {
		return a.text("ทำไม่สำเร็จครับ: " + err.Error())
	}
	a.logActivity(ctx, pa.Name, activitySummary(pa.Name, r), pv.Plan.ProjectID, map[string]any{"result": r})
	links := a.resultLinks(r)
	title, _ := r["summary"].(string)
	if title == "" {
		title = fmt.Sprintf("เรียบร้อย %d รายการ", max(1, len(links)))
	}
	if len(links) > 0 {
		return []port.Message{a.View.Result(title, links)}
	}
	return a.text(title)
}

func (a *App) handleReminderButton(ctx context.Context, kind string, id int64) []port.Message {
	r, err := a.Records.Get(ctx, a.ws(), id)
	if err != nil || r == nil || r.Kind != domain.KindReminder {
		return a.text("ไม่พบการเตือนนี้แล้วครับ")
	}
	var d domain.ReminderData
	r.Decode(&d)
	if kind == "rcancel" || kind == "rdone" {
		st := "cancelled"
		if kind == "rdone" {
			st = "done"
		}
		if _, err := a.Records.Update(ctx, a.ws(), id, domain.RecordPatch{Status: &st}); err != nil {
			return a.text(a.errText(err))
		}
		if kind == "rdone" {
			return a.text("ปิดการเตือนแล้วครับ")
		}
		return a.text(fmt.Sprintf("ยกเลิกการเตือน \"%s\" แล้วครับ", d.Text))
	}
	base := a.Now()
	if at, ok := domain.ParseInstant(d.At); ok && at.After(base) {
		base = at
	}
	next := base.Add(24 * time.Hour)
	open := "open"
	if _, err := a.Records.Update(ctx, a.ws(), id, domain.RecordPatch{
		Status: &open, DueAt: &next, SetDueAt: true,
		Data: map[string]any{"at": next.UTC().Format("2006-01-02T15:04:05.000Z")},
	}); err != nil {
		return a.text(a.errText(err))
	}
	return a.text("เลื่อนไปเตือน " + domain.ThaiDate(domain.DateIn(next, a.Loc), true) + " แล้วครับ")
}

func (a *App) errText(err error) string {
	if domain.IsExpected(err) {
		return err.Error()
	}
	slog.Error("request failed", "err", err)
	return "ขออภัยครับ เกิดข้อผิดพลาด: " + err.Error()
}

// resultLinks lists the links of a write result for the result card.
func (a *App) resultLinks(r Result) []domain.Link {
	links := []domain.Link{}
	if cs, ok := r["created"].([]itemRef); ok {
		for _, c := range cs {
			links = append(links, domain.Link{Label: c.Key + " " + c.Name, URL: c.URL})
		}
	}
	if u, ok := r["updated"].(itemRef); ok {
		links = append(links, domain.Link{Label: u.Key + " " + u.Name, URL: u.URL})
	}
	ls, hasLinks := r["links"].([]domain.Link)
	if c, ok := r["commented"].(string); ok && c != "" && !hasLinks {
		links = append(links, domain.Link{Label: "คอมเมนต์ใน " + c, URL: a.tasks().KeyURL(c)})
	}
	for _, l := range ls {
		if l.URL != "" {
			links = append(links, l)
		}
	}
	return links
}

func activitySummary(name string, r Result) string {
	if cs, ok := r["created"].([]itemRef); ok && len(cs) > 0 {
		parts := make([]string, len(cs))
		for i, c := range cs {
			parts[i] = c.Key + " " + c.Name
		}
		return "สร้าง " + strings.Join(parts, ", ")
	}
	if u, ok := r["updated"].(itemRef); ok {
		return "แก้ " + u.Key + " " + u.Name
	}
	if s, ok := r["summary"].(string); ok && s != "" {
		return s
	}
	if c, ok := r["commented"].(string); ok && c != "" {
		return "คอมเมนต์ใน " + c
	}
	if rec, ok := r["record"]; ok && rec != nil {
		return "บันทึก " + fmt.Sprint(rec)
	}
	return name
}

// ---------------------------------------------------------------- internals

// appendUser adds user content, merging into the previous user message if there is one.
func (a *App) appendUser(blocks ...domain.Block) {
	now := a.nowMs()
	a.Store.Update(func(s *domain.AppState) {
		msgs := s.Conversation.Messages
		if n := len(msgs); n > 0 && msgs[n-1].Role == "user" {
			msgs[n-1].Content = append(msgs[n-1].Content, blocks...)
		} else {
			s.Conversation.Messages = append(msgs, domain.ChatMessage{Role: "user", Content: blocks})
		}
		s.Conversation.LastAt = now
	})
}

func toolResult(id string, content string, isErr bool) domain.Block {
	b := domain.Block{"type": "tool_result", "tool_use_id": id, "content": content}
	if isErr {
		b["is_error"] = true
	}
	return b
}

// closePending answers a pending card's tool calls with note (not done) and drops the card.
func (a *App) closePending(note string) {
	var p *domain.PendingAction
	a.Store.Update(func(s *domain.AppState) { p, s.Pending = s.Pending, nil })
	if p == nil {
		return
	}
	blocks := append([]domain.Block{}, p.ReadyResults...)
	for _, w := range p.Writes {
		blocks = append(blocks, toolResult(w.ToolUseID, note, false))
	}
	a.appendUser(blocks...)
}

func (a *App) confirmPending(ctx context.Context, p domain.PendingAction) []port.Message {
	results := []domain.Block{}
	links := []domain.Link{}
	errs := []string{}
	for _, w := range p.Writes {
		r, err := a.ExecuteWrite(ctx, w.Preview.Plan)
		if err != nil {
			errs = append(errs, err.Error())
			results = append(results, toolResult(w.ToolUseID, "ทำไม่สำเร็จ: "+err.Error(), true))
			continue
		}
		full := Result{"ok": true}
		for k, v := range r {
			full[k] = v
		}
		results = append(results, toolResult(w.ToolUseID, toJSON(full), false))
		a.logActivity(ctx, w.Name, activitySummary(w.Name, r), w.Preview.Plan.ProjectID, map[string]any{"input": w.Input, "result": r})
		if ls := a.resultLinks(r); len(ls) > 0 {
			links = append(links, ls...)
		} else {
			label, _ := r["summary"].(string)
			if label == "" {
				label = "เรียบร้อย"
			}
			links = append(links, domain.Link{Label: label, URL: a.DashboardURL("")})
		}
	}
	a.Store.Update(func(s *domain.AppState) { s.Pending = nil })
	a.appendUser(append(append([]domain.Block{}, p.ReadyResults...), results...)...)
	out := []port.Message{}
	if len(links) > 0 {
		out = append(out, a.View.Result(fmt.Sprintf("เรียบร้อย %d รายการ", len(links)), links))
	}
	if len(errs) > 0 {
		out = append(out, a.View.Text("มีบางรายการไม่สำเร็จครับ\n• "+strings.Join(errs, "\n• ")))
	}
	return out
}

func lastN(msgs []port.Message, n int) []port.Message {
	if len(msgs) > n {
		return msgs[len(msgs)-n:]
	}
	return msgs
}

func (a *App) runLoop(ctx context.Context) []port.Message {
	key := a.claudeKey()
	if key == "" {
		return a.text("ยังไม่ได้ใส่ Claude API key ครับ ใส่ได้ที่หน้าตั้งค่า\n" + a.SettingsURL(""))
	}
	out := []port.Message{}
	t := &turn{}
	statusProjects := []string{}
	needProject := false
	rounds := a.Cfg.ClaudeMaxToolRounds
	if rounds <= 0 {
		rounds = 8
	}
	for round := 0; round < rounds; round++ {
		snap := a.Store.Snapshot()
		current := ""
		if snap.CurrentProjectID != nil {
			if ps, err := a.tasks().Projects(ctx); err == nil {
				for _, p := range ps {
					if p.ID == *snap.CurrentProjectID {
						current = p.Name
					}
				}
			}
		}
		resp, err := a.LLM.Create(ctx, key, port.LLMRequest{
			Model:     a.Cfg.ClaudeModel,
			MaxTokens: 4096,
			System: []domain.Block{
				{"type": "text", "text": systemPrompt, "cache_control": map[string]any{"type": "ephemeral"}},
				domain.TextBlock(a.dynamicContext(current)),
			},
			Tools:    a.ToolDefs(),
			Messages: snap.Conversation.Messages,
			Effort:   a.Cfg.ClaudeEffort,
		})
		if err != nil {
			slog.Error("Claude API error", "err", err)
			return a.text("ขออภัยครับ ระบบ AI ขัดข้องชั่วคราว ลองใหม่อีกครั้งได้ไหมครับ")
		}
		a.trackUsage(ctx, resp.Usage)
		now := a.nowMs()
		a.Store.Update(func(s *domain.AppState) {
			s.Conversation.Messages = append(s.Conversation.Messages, domain.ChatMessage{Role: "assistant", Content: resp.Content})
			s.Conversation.LastAt = now
		})

		var texts []string
		type toolUse struct {
			id, name string
			input    json.RawMessage
		}
		var uses []toolUse
		for _, b := range resp.Content {
			switch b["type"] {
			case "text":
				if s, ok := b["text"].(string); ok {
					texts = append(texts, s)
				}
			case "tool_use":
				id, _ := b["id"].(string)
				name, _ := b["name"].(string)
				in, _ := json.Marshal(b["input"])
				uses = append(uses, toolUse{id, name, in})
			}
		}
		textOut := strings.TrimSpace(strings.Join(texts, "\n"))
		if resp.StopReason != "tool_use" || len(uses) == 0 {
			return a.finish(ctx, append(out, t.cards...), textOut, statusProjects, needProject)
		}

		ready := []domain.Block{}
		writes := []domain.PendingWrite{}
		for _, tu := range uses {
			if a.IsWriteTool(tu.name) {
				pv, err := a.PrepareWrite(ctx, tu.name, tu.input)
				if err == nil {
					writes = append(writes, domain.PendingWrite{ToolUseID: tu.id, Name: tu.name, Input: tu.input, Preview: pv})
					continue
				}
				ready = append(ready, a.toolError(tu.name, tu.id, err, &needProject))
				continue
			}
			res, err := a.RunReadTool(ctx, t, tu.name, tu.input)
			if err != nil {
				ready = append(ready, a.toolError(tu.name, tu.id, err, &needProject))
				continue
			}
			if tu.name == "project_status" {
				var in struct{ Project string }
				_ = json.Unmarshal(tu.input, &in)
				if p, err := a.ResolveProject(ctx, in.Project); err == nil && !containsStr(statusProjects, p.ID) {
					statusProjects = append(statusProjects, p.ID)
				}
			}
			ready = append(ready, toolResult(tu.id, toJSON(res), false))
		}

		if len(writes) > 0 {
			id := randomHex(6)
			a.Store.Update(func(s *domain.AppState) {
				s.Pending = &domain.PendingAction{ID: id, CreatedAt: now, ReadyResults: ready, Writes: writes}
			})
			if textOut != "" {
				out = append(out, a.View.Text(textOut))
			}
			out = append(out, t.cards...)
			pvs := make([]domain.Preview, len(writes))
			for i, w := range writes {
				pvs[i] = w.Preview
			}
			merged := domain.MergePreviews(pvs)
			pu := ""
			if merged.FullHTML != "" {
				pu = a.PreviewURL(id)
			}
			out = append(out, a.View.Confirm(merged, id, false, pu))
			return lastN(out, maxReplyMsg)
		}
		if textOut != "" {
			out = append(out, a.View.Text(textOut))
		}
		a.appendUser(ready...)
	}
	out = append(out, t.cards...)
	out = append(out, a.View.Text("คำสั่งนี้ซับซ้อนเกินไปสำหรับรอบเดียวครับ ลองแบ่งเป็นขั้น ๆ ได้ไหมครับ"))
	return lastN(out, maxReplyMsg)
}

func containsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (a *App) toolError(name, id string, err error, needProject *bool) domain.Block {
	msg := err.Error()
	if isNeedProject(err) {
		*needProject = true
	}
	var nc *domain.NotConnectedError
	if !domain.IsExpected(err) && !errors.As(err, &nc) && !strings.Contains(msg, "ยังไม่ได้เชื่อม") {
		slog.Error("tool failed", "tool", name, "err", err)
	}
	return toolResult(id, msg, true)
}

func (a *App) finish(ctx context.Context, msgs []port.Message, text string, statusProjects []string, needProject bool) []port.Message {
	var ps []domain.Project
	if len(statusProjects) > 0 || needProject {
		ps, _ = a.tasks().Projects(ctx)
	}
	for _, pid := range statusProjects {
		for _, p := range ps {
			if p.ID != pid {
				continue
			}
			if m, err := a.ProjectMetrics(ctx, p); err == nil {
				msgs = append(msgs, a.View.Status(m, a.DashboardURL(p.ID)))
			}
		}
	}
	if needProject {
		if text == "" {
			text = "หมายถึงโปรเจกต์ไหนครับ"
		}
		msgs = append(msgs, a.View.ProjectPicker(text, ps))
	} else if text != "" {
		msgs = append(msgs, a.View.Text(text))
	}
	if len(msgs) == 0 {
		msgs = append(msgs, a.View.Text("รับทราบครับ"))
	}
	return lastN(msgs, maxReplyMsg)
}

func (a *App) trackUsage(ctx context.Context, u port.LLMUsage) {
	in := u.InputTokens + u.CacheCreationTokens
	a.History.LogUsage(ctx, a.Cfg.ClaudeModel, in, u.OutputTokens, u.CacheReadTokens)
	month := a.today()[:7]
	a.Store.Update(func(s *domain.AppState) {
		if s.Usage.Month != month {
			s.Usage = domain.Usage{Month: month}
		}
		s.Usage.InputTokens += in
		s.Usage.OutputTokens += u.OutputTokens
		s.Usage.CacheReadTokens += u.CacheReadTokens
	})
}
