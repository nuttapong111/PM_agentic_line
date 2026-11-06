package line

import (
	"fmt"
	"math"
	"strings"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

// Presenter builds LINE Flex messages from the use cases' view models.
type Presenter struct{}

type box = map[string]any

const (
	accent = "#2448C8"
	muted  = "#5A6573"
	red    = "#A2231B"
	amber  = "#8A4300"
	green  = "#1B6B34"
)

func cut(s string, n int) string { return domain.Truncate(s, n) }

func text(t string, o box) box {
	if t == "" {
		t = " "
	}
	b := box{"type": "text", "text": t, "wrap": true, "size": "sm", "color": "#18202B"}
	for k, v := range o {
		b[k] = v
	}
	return b
}

func sep() box { return box{"type": "separator", "margin": "md", "color": "#E6EAF0"} }

func button(label string, action box, primary bool) box {
	a := box{"label": cut(label, 40)}
	for k, v := range action {
		a[k] = v
	}
	b := box{"type": "button", "style": "secondary", "height": "sm", "action": a}
	if primary {
		b["style"], b["color"] = "primary", accent
	}
	return b
}

func uriBtn(label, url string, primary bool) box {
	return button(label, box{"type": "uri", "uri": url}, primary)
}

func postbackBtn(label, data string, primary bool, displayText string) box {
	if displayText == "" {
		displayText = label
	}
	return button(label, box{"type": "postback", "data": data, "displayText": displayText}, primary)
}

func bar(percent float64, color string) box {
	p := int(math.Max(0, math.Min(100, math.Round(percent))))
	return box{
		"type": "box", "layout": "vertical", "height": "8px", "backgroundColor": "#E6EAF0", "cornerRadius": "4px", "margin": "sm",
		"contents": []box{{"type": "box", "layout": "vertical", "width": fmt.Sprintf("%d%%", max(p, 1)), "height": "8px", "backgroundColor": color, "cornerRadius": "4px", "contents": []box{}}},
	}
}

func stat(n int, label, color, bg string) box {
	return box{
		"type": "box", "layout": "vertical", "backgroundColor": bg, "cornerRadius": "8px", "paddingAll": "8px", "flex": 1,
		"contents": []box{text(fmt.Sprint(n), box{"size": "lg", "weight": "bold", "color": color}), text(label, box{"size": "xs", "color": color})},
	}
}

func itemLine(v domain.ItemView, extra string) box {
	t := v.Key + " " + v.Name
	if extra != "" {
		t += " · " + extra
	}
	return text(t, box{"size": "sm", "maxLines": 2})
}

func bubble(header []box, body []box, footer []box, headerBg string) box {
	b := box{"type": "bubble", "size": "mega", "body": box{"type": "box", "layout": "vertical", "spacing": "sm", "paddingAll": "14px", "contents": body}}
	if header != nil {
		if headerBg == "" {
			headerBg = "#EEF2FD"
		}
		b["header"] = box{"type": "box", "layout": "vertical", "spacing": "xs", "paddingAll": "14px", "backgroundColor": headerBg, "contents": header}
	}
	if footer != nil {
		b["footer"] = box{"type": "box", "layout": "vertical", "spacing": "sm", "paddingAll": "14px", "contents": footer}
	}
	return b
}

func flex(alt string, contents box) port.Message {
	return port.Message{"type": "flex", "altText": cut(alt, 390), "contents": contents}
}

// ---------------------------------------------------------------- cards

func (Presenter) Text(t string) port.Message {
	return port.Message{"type": "text", "text": cut(t, 5000)}
}

func (Presenter) Status(m domain.ProjectMetrics, dashboardURL string) port.Message {
	c := m.Cycle
	body := []box{}
	if c != nil {
		left := " · ตามแผน"
		col := muted
		if c.ProjectedMiss > 0 {
			left = fmt.Sprintf(" · ตาม velocity คาดว่าหลุด %d งาน", c.ProjectedMiss)
			col = amber
		}
		body = append(body,
			box{"type": "box", "layout": "horizontal", "contents": []box{
				text(fmt.Sprintf("เสร็จ %d จาก %d งาน", c.Done, c.Total), box{"color": muted, "flex": 1}),
				text(fmt.Sprintf("%d%%", c.Percent), box{"weight": "bold", "size": "lg", "align": "end", "flex": 0}),
			}},
			bar(float64(c.Percent), accent),
			text(fmt.Sprintf("เหลือ %d วัน%s", c.DaysLeft, left), box{"size": "xs", "color": col}),
		)
		if c.AddedMidway > 0 {
			body = append(body, text(fmt.Sprintf("เพิ่มกลาง sprint %d งาน", c.AddedMidway), box{"size": "xs", "color": muted}))
		}
	}
	body = append(body, box{"type": "box", "layout": "horizontal", "spacing": "sm", "margin": "md", "contents": []box{
		stat(m.Counts.Overdue, "เลยกำหนด", red, "#FDECEA"), stat(m.Counts.Blocked, "Blocked", amber, "#FFF1E0"), stat(m.Counts.Unassigned, "ไม่มีเจ้าของ", "#2E3947", "#EEF1F5"),
	}})
	var first *domain.ItemView
	if len(m.Overdue) > 0 {
		first = &m.Overdue[0]
	} else if len(m.Blocked) > 0 {
		first = &m.Blocked[0]
	}
	if first != nil {
		label := "ควรดูก่อน · Blocked"
		if first.DaysOverdue > 0 {
			label = fmt.Sprintf("ควรดูก่อน · เลยกำหนด %d วัน", first.DaysOverdue)
		}
		body = append(body, sep(), text(label, box{"size": "xs", "weight": "bold", "color": red, "margin": "md"}), itemLine(*first, ""))
	}
	alt := m.Project.Name + ": "
	head1 := m.Project.Name + " · ภาพรวม"
	head2 := fmt.Sprintf("งานเปิด %d งาน", m.Counts.Open)
	if c != nil {
		alt += fmt.Sprintf("%d%% ", c.Percent)
		head1 = m.Project.Name + " · Cycle ปัจจุบัน"
		head2 = fmt.Sprintf("%s · %s – %s", c.Name, domain.ThaiDate(c.Start, false), domain.ThaiDate(c.End, false))
	}
	alt += fmt.Sprintf("เลยกำหนด %d", m.Counts.Overdue)
	footer := []box{}
	if m.Counts.Overdue > 0 {
		footer = append(footer, postbackBtn("ดูงานที่เลยกำหนด", "cmd:overdue", true, "งานไหนเลยกำหนดบ้าง"))
	}
	footer = append(footer, uriBtn("Dashboard", dashboardURL, m.Counts.Overdue == 0))
	return flex(alt, bubble([]box{text(head1, box{"size": "xs", "color": muted}), text(head2, box{"weight": "bold", "size": "md"})}, body, footer, ""))
}

func (Presenter) Digest(list []domain.ProjectMetrics, dateLabel, dashboardURL string, meetings []string) port.Message {
	var overdue, today []domain.ItemView
	for _, m := range list {
		overdue = append(overdue, m.Overdue...)
		today = append(today, m.DueToday...)
	}
	body := []box{}
	section := func(title, color string, items []box) {
		if len(body) > 0 {
			body = append(body, sep())
		}
		body = append(body, text(title, box{"size": "xs", "weight": "bold", "color": color, "margin": "md"}))
		body = append(body, items...)
	}
	var lines []box
	for _, v := range overdue[:min(3, len(overdue))] {
		lines = append(lines, itemLine(v, fmt.Sprintf("%d วัน", v.DaysOverdue)))
	}
	col := muted
	if len(overdue) > 0 {
		col = red
	}
	section(fmt.Sprintf("เลยกำหนด %d", len(overdue)), col, lines)
	if len(overdue) > 3 {
		body = append(body, text(fmt.Sprintf("+ อีก %d งาน", len(overdue)-3), box{"size": "xs", "color": accent}))
	}
	lines = nil
	for _, v := range today[:min(3, len(today))] {
		lines = append(lines, itemLine(v, ""))
	}
	section(fmt.Sprintf("ครบกำหนดวันนี้ %d", len(today)), "#3D4855", lines)
	if len(meetings) > 0 {
		lines = nil
		for _, t := range meetings {
			lines = append(lines, text(t, nil))
		}
		section(fmt.Sprintf("ประชุมวันนี้ %d", len(meetings)), "#3D4855", lines)
	}
	first := true
	for _, m := range list {
		if m.Cycle == nil {
			continue
		}
		if first {
			body = append(body, sep())
			first = false
		}
		body = append(body, box{"type": "box", "layout": "horizontal", "margin": "md", "contents": []box{
			text(m.Project.Name, box{"flex": 1}), text(fmt.Sprintf("%d%%", m.Cycle.Percent), box{"weight": "bold", "align": "end", "flex": 0}),
		}}, bar(float64(m.Cycle.Percent), accent))
	}
	return flex(fmt.Sprintf("สรุปเช้านี้: เลยกำหนด %d, ครบวันนี้ %d", len(overdue), len(today)), bubble(
		[]box{text("สรุปเช้านี้", box{"size": "xs", "color": "#DCE4FB"}), text(dateLabel, box{"weight": "bold", "size": "lg", "color": "#FFFFFF"})},
		body,
		[]box{{"type": "box", "layout": "horizontal", "spacing": "sm", "contents": []box{postbackBtn("งานของฉัน", "cmd:mine", true, "งานของฉันมีอะไรบ้าง"), uriBtn("Dashboard", dashboardURL, false)}}},
		accent,
	))
}

// Confirm builds a confirm card. Proactive cards (sent by the bot on its own) use pconfirm/pedit/pcancel.
func (Presenter) Confirm(p domain.Preview, id string, proactive bool, previewURL string) port.Message {
	pre := ""
	if proactive {
		pre = "p"
	}
	body := []box{}
	for _, l := range p.Lines {
		contents := []box{}
		if l.Tag != "" {
			contents = append(contents, box{"type": "box", "layout": "vertical", "backgroundColor": "#E8EDFC", "cornerRadius": "6px", "width": "44px", "justifyContent": "center",
				"contents": []box{text(l.Tag, box{"size": "xxs", "weight": "bold", "align": "center", "color": "#2A3A7A"})}})
		}
		col := muted
		if l.Warn {
			col = amber
		}
		contents = append(contents, box{"type": "box", "layout": "vertical", "flex": 1, "contents": []box{text(l.Text, box{"weight": "bold"}), text(l.Sub, box{"size": "xs", "color": col})}})
		body = append(body, box{"type": "box", "layout": "horizontal", "spacing": "md", "paddingTop": "6px", "paddingBottom": "6px", "contents": contents})
	}
	if len(p.Warnings) > 0 {
		body = append(body, box{"type": "box", "layout": "vertical", "backgroundColor": "#FFF1E0", "cornerRadius": "8px", "paddingAll": "8px", "margin": "md",
			"contents": []box{text(strings.Join(p.Warnings, "\n"), box{"size": "xs", "color": "#6B3400"})}})
	}
	kicker := p.Kicker
	if kicker == "" {
		kicker = "รอยืนยัน"
	}
	cancel := p.CancelLabel
	if cancel == "" {
		cancel = "ยกเลิก"
	}
	footer := []box{postbackBtn(p.ConfirmLabel, pre+"confirm:"+id, true, "")}
	if previewURL != "" {
		footer = append(footer, uriBtn("ดูฉบับเต็ม", previewURL, false))
	}
	footer = append(footer, box{"type": "box", "layout": "horizontal", "spacing": "sm", "contents": []box{
		postbackBtn("แก้ไข", pre+"edit:"+id, false, "ขอแก้ไข"), postbackBtn(cancel, pre+"cancel:"+id, false, ""),
	}})
	return flex("รอยืนยัน: "+p.Title, bubble(
		[]box{text(kicker, box{"size": "xs", "weight": "bold", "color": "#2A3A7A"}), text(p.Title, box{"weight": "bold", "size": "md"}), text(p.Subtitle, box{"size": "xs", "color": "#4A5568"})},
		body, footer, ""))
}

func (Presenter) Result(title string, links []domain.Link) port.Message {
	body := []box{text(title, box{"weight": "bold", "color": green})}
	for _, l := range links {
		t := text(l.Label, box{"color": accent, "decoration": "underline"})
		t["action"] = box{"type": "uri", "uri": l.URL}
		body = append(body, t)
	}
	return flex(title, bubble(nil, body, nil, ""))
}

func (Presenter) ProjectPicker(prompt string, projects []domain.Project) port.Message {
	items := []box{}
	for _, p := range projects[:min(12, len(projects))] {
		items = append(items, box{"type": "action", "action": box{"type": "postback", "label": cut(p.Name, 20), "data": "project:" + p.ID, "displayText": p.Name}})
	}
	return port.Message{"type": "text", "text": prompt, "quickReply": box{"items": items}}
}

func (Presenter) QuickText(t string, items []domain.QuickItem) port.Message {
	out := []box{}
	for _, i := range items[:min(13, len(items))] {
		dt := i.DisplayText
		if dt == "" {
			dt = i.Label
		}
		out = append(out, box{"type": "action", "action": box{"type": "postback", "label": cut(i.Label, 20), "data": cut(i.Data, 300), "displayText": cut(dt, 300)}})
	}
	return port.Message{"type": "text", "text": cut(t, 5000), "quickReply": box{"items": out}}
}

// ---------------------------------------------------------------- generic info card

var toneColor = map[string]string{domain.ColorRed: red, domain.ColorAmber: amber, domain.ColorGreen: green, domain.ColorMuted: muted}
var headBg = map[string]string{domain.ToneInfo: "#EEF2FD", domain.ToneWarn: "#FFF1E0", domain.ToneDanger: "#FDECEA", domain.ToneOK: "#E7F4EC"}
var headText = map[string]string{domain.ToneInfo: "#2A3A7A", domain.ToneWarn: "#6B3400", domain.ToneDanger: red, domain.ToneOK: green}

func toButton(b domain.Btn) box {
	if b.URL != "" {
		return uriBtn(b.Label, b.URL, b.Primary)
	}
	return postbackBtn(b.Label, b.Data, b.Primary, b.DisplayText)
}

func (Presenter) Info(c domain.InfoCard) port.Message {
	tone := c.Tone
	if tone == "" {
		tone = domain.ToneInfo
	}
	body := []box{}
	for _, r := range c.Rows {
		col := "#18202B"
		if r.Color != "" {
			col = toneColor[r.Color]
		}
		body = append(body, box{"type": "box", "layout": "horizontal", "spacing": "md", "contents": []box{
			text(r.Label, box{"size": "xs", "color": muted, "flex": 2}), text(r.Value, box{"size": "sm", "flex": 5, "weight": "bold", "color": col}),
		}})
	}
	for _, b := range c.Bars {
		col, valCol := accent, "#18202B"
		if b.Tone != "" {
			col = toneColor[b.Tone]
			valCol = col
		}
		v := b.Value
		if v == "" {
			v = fmt.Sprintf("%d%%", b.Percent)
		}
		body = append(body, box{"type": "box", "layout": "horizontal", "margin": "md", "contents": []box{
			text(b.Label, box{"flex": 1, "size": "sm"}), text(v, box{"flex": 0, "align": "end", "weight": "bold", "color": valCol}),
		}}, bar(math.Min(100, float64(b.Percent)), col))
	}
	for _, s := range c.Sections {
		if len(s.Lines) == 0 {
			continue
		}
		if len(body) > 0 {
			body = append(body, sep())
		}
		col := "#3D4855"
		if s.Tone != "" {
			col = toneColor[s.Tone]
		}
		body = append(body, text(s.Title, box{"size": "xs", "weight": "bold", "color": col, "margin": "md"}))
		for _, l := range s.Lines[:min(8, len(s.Lines))] {
			body = append(body, text(l, box{"size": "sm"}))
		}
		if len(s.Lines) > 8 {
			body = append(body, text(fmt.Sprintf("+ อีก %d รายการ", len(s.Lines)-8), box{"size": "xs", "color": accent}))
		}
	}
	if c.Note != "" {
		body = append(body, box{"type": "box", "layout": "vertical", "backgroundColor": "#F4F6F9", "cornerRadius": "8px", "paddingAll": "8px", "margin": "md",
			"contents": []box{text(c.Note, box{"size": "xs", "color": "#3D4855"})}})
	}
	if len(body) == 0 {
		body = append(body, text(" ", nil))
	}
	var footer []box
	if len(c.Buttons) > 0 {
		footer = append(footer, toButton(c.Buttons[0]))
		rest := c.Buttons[1:min(5, len(c.Buttons))]
		for i := 0; i < len(rest); i += 2 {
			row := []box{}
			for _, b := range rest[i:min(i+2, len(rest))] {
				row = append(row, toButton(b))
			}
			footer = append(footer, box{"type": "box", "layout": "horizontal", "spacing": "sm", "contents": row})
		}
	}
	header := []box{text(c.Kicker, box{"size": "xs", "weight": "bold", "color": headText[tone]}), text(c.Title, box{"weight": "bold", "size": "md"})}
	if c.Subtitle != "" {
		header = append(header, text(c.Subtitle, box{"size": "xs", "color": "#4A5568"}))
	}
	return flex(c.Kicker+": "+c.Title, bubble(header, body, footer, headBg[tone]))
}
