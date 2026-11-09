package usecase

import "github.com/nuttapong111/pm_agentic_line/internal/port"

// Tool schemas the agent sees. Read tools run at once; write tools return a preview and run only
// after the owner taps ยืนยัน on the confirm card.

type schema = map[string]any

func obj(props schema, required ...string) schema {
	if required == nil {
		required = []string{}
	}
	return schema{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func str(desc string) schema {
	if desc == "" {
		return schema{"type": "string"}
	}
	return schema{"type": "string", "description": desc}
}

var (
	coreProject    = str(`ชื่อหรือรหัสโปรเจกต์ (เช่น FDABIGDATA หรือ "FDA Big data"). เว้นไว้ = โปรเจกต์ปัจจุบัน`)
	featureProject = str("ชื่อหรือรหัสโปรเจกต์ เว้นไว้ = โปรเจกต์ปัจจุบัน")
	level          = schema{"type": "string", "enum": []string{"high", "medium", "low"}}
	priority       = schema{"type": "string", "enum": []string{"urgent", "high", "medium", "low", "none"}}
	boolean        = schema{"type": "boolean"}
	strArray       = schema{"type": "array", "items": schema{"type": "string"}}
)

func tool(name, desc string, input schema) port.ToolDef {
	return port.ToolDef{Name: name, Description: desc, InputSchema: input}
}

// coreObj is an object schema without "required" (as the core tools were declared).
func coreObj(props schema, required ...string) schema {
	s := schema{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var coreTools = []port.ToolDef{
	tool("list_projects", "รายชื่อโปรเจกต์ในระบบจัดการงานที่ติดตามอยู่ พร้อมบอกว่าอันไหนเป็นโปรเจกต์ปัจจุบัน", coreObj(schema{})),
	tool("set_current_project", "ตั้งโปรเจกต์ปัจจุบัน ใช้เมื่อผู้ใช้บอกให้สลับโปรเจกต์", coreObj(schema{"project": coreProject}, "project")),
	tool("project_status", "ภาพรวมโปรเจกต์: cycle ปัจจุบัน (ความคืบหน้า, คาดว่าหลุดกี่งาน, งานที่เพิ่มกลาง sprint), งานเลยกำหนด, blocked, ค้างนาน, งานต่อคน, module เทียบกำหนดส่ง, bug ตาม priority. ตัวเลขคำนวณจากข้อมูลจริงแล้ว ให้ใช้ตามนั้น",
		coreObj(schema{"project": coreProject})),
	tool("search_work_items", "ค้นหางานในโปรเจกต์ด้วยเงื่อนไข คืนรายการงานพร้อมรหัส สถานะ ผู้รับผิดชอบ กำหนดส่ง", coreObj(schema{
		"project":          coreProject,
		"text":             str("คำค้นในชื่องาน"),
		"assignee":         str(`ชื่อหรือชื่อเล่นผู้รับผิดชอบ, "me" = ผู้ใช้เอง, "none" = ไม่มีเจ้าของ`),
		"status":           schema{"type": "string", "enum": []string{"open", "overdue", "due_today", "due_7_days", "blocked", "done", "all"}, "description": "ค่าเริ่มต้น open"},
		"in_current_cycle": boolean,
		"limit":            schema{"type": "integer", "minimum": 1, "maximum": 30, "description": "ค่าเริ่มต้น 10"},
	})),
	tool("get_work_item", "รายละเอียดงานจากรหัส เช่น FDABIGDATA-123 (รู้โปรเจกต์จากรหัสเอง)", coreObj(schema{"key": str("")}, "key")),
	tool("list_members_and_states", "สมาชิกและสถานะ (state) ที่ใช้ได้ในโปรเจกต์ ใช้เมื่อต้องเลือกคนหรือสถานะแต่ไม่แน่ใจชื่อ", coreObj(schema{"project": coreProject})),
	tool("create_work_items", "สร้างงานใหม่ 1 ถึง 10 งาน (ต้องให้ผู้ใช้ยืนยันก่อน ระบบจะส่งการ์ดยืนยันเอง). ถ้าผู้ใช้ขอให้แตกงานย่อย ให้ใส่งานแม่เป็นรายการแรกและใส่ parent_index: 0 ในงานย่อย. วันที่ใช้รูปแบบ YYYY-MM-DD",
		coreObj(schema{
			"project":              coreProject,
			"add_to_current_cycle": schema{"type": "boolean", "description": "ค่าเริ่มต้น true"},
			"module":               str("ชื่อ module ที่จะใส่ทุกงานเข้าไป เว้นได้ (ใส่เฉพาะเมื่อผู้ใช้บอก)"),
			"items": schema{"type": "array", "minItems": 1, "maxItems": 10, "items": coreObj(schema{
				"name":         str(""),
				"description":  str(""),
				"assignee":     str("ชื่อหรือชื่อเล่น เว้นได้"),
				"priority":     priority,
				"state":        str("ชื่อสถานะ เว้นไว้ = ค่าเริ่มต้นของโปรเจกต์"),
				"start_date":   str(""),
				"target_date":  str(""),
				"parent_index": schema{"type": "integer", "minimum": 0},
				"tag":          str("ป้ายสั้นสำหรับการ์ด เช่น BE, FE, QA"),
			}, "name")},
		}, "items")),
	tool("update_work_item", "แก้งานที่มีอยู่ (ต้องยืนยันก่อน): เปลี่ยนชื่อ สถานะ ผู้รับผิดชอบ priority วันที่ ย้ายเข้า cycle ปัจจุบัน ใส่เข้า module", coreObj(schema{
		"key":                   str("รหัสงาน เช่น PEALICENSE-27"),
		"name":                  str(""),
		"state":                 str("ชื่อสถานะ เช่น In Progress, Done, Blocked"),
		"assignee":              str(`ชื่อหรือชื่อเล่น; "none" = เอาออก`),
		"priority":              priority,
		"start_date":            str(""),
		"target_date":           str(""),
		"move_to_current_cycle": boolean,
		"module":                str("ชื่อ module ที่จะใส่งานนี้เข้าไป"),
	}, "key")),
	tool("create_project", "สร้างโปรเจกต์ใหม่ในระบบจัดการงาน (ต้องยืนยันก่อน) ผู้ใช้เป็น Admin และเพิ่มสมาชิกจาก workspace ได้เลย identifier เป็นอักษรอังกฤษ/ตัวเลขไม่เกิน 12 ตัว ถ้าผู้ใช้ไม่บอกให้คิดจากชื่อ",
		obj(schema{
			"name":        str(""),
			"identifier":  str("เช่น PEA3"),
			"description": str(""),
			"members":     schema{"type": "array", "items": str("ชื่อ ชื่อเล่น หรืออีเมล")},
			"role":        str("member (ค่าเริ่มต้น), admin หรือ guest"),
		}, "name")),
	tool("add_project_members", "เพิ่มคนใน workspace เข้าโปรเจกต์ (ต้องยืนยันก่อน) ถ้าระบบไม่ยอมให้เพิ่มผ่าน API จะได้ลิงก์ให้ผู้ใช้กดเพิ่มเอง",
		coreObj(schema{
			"project": coreProject,
			"members": schema{"type": "array", "items": str("ชื่อ ชื่อเล่น หรืออีเมล")},
			"role":    str("member (ค่าเริ่มต้น), admin หรือ guest"),
		}, "members")),
	tool("create_cycle", "สร้าง sprint (cycle) ใหม่ในโปรเจกต์ (ต้องยืนยันก่อน) และย้ายงานที่ระบุเข้าไปได้ วันที่ใช้รูปแบบ YYYY-MM-DD ถ้าผู้ใช้ไม่บอกชื่อ ให้ตั้งตามลำดับ sprint เดิม เช่น Sprint 5",
		coreObj(schema{
			"project":     coreProject,
			"name":        str(""),
			"description": str("เป้าหมายของ sprint เว้นได้"),
			"start_date":  str(""),
			"end_date":    str(""),
			"item_keys":   schema{"type": "array", "items": str("รหัสงาน เช่น PEALICENSE-27")},
		}, "name", "start_date", "end_date")),
	tool("set_module_dates", "ตั้งหรือแก้วันเริ่มและวันจบ (target) ของ module ในโปรเจกต์ ทำได้หลาย module ในครั้งเดียว (ต้องยืนยันก่อน) วันที่ใช้รูปแบบ YYYY-MM-DD เว้นวันที่ไม่เปลี่ยน",
		coreObj(schema{
			"project": coreProject,
			"modules": schema{"type": "array", "items": obj(schema{
				"module":      str("ชื่อ module"),
				"start_date":  str(""),
				"target_date": str("วันจบหรือกำหนดส่งของ module"),
			}, "module")},
		}, "modules")),
	tool("add_comment", "เพิ่มคอมเมนต์ในงาน (ต้องยืนยันก่อน)", coreObj(schema{"key": str(""), "text": str("")}, "key", "text")),
	tool("dashboard_link", "ลิงก์หน้า dashboard บนเว็บ", coreObj(schema{"project": coreProject})),
}

var featureTools = []port.ToolDef{
	tool("log_risk", "บันทึกความเสี่ยง (type=risk) หรือการตัดสินใจ (type=decision) ลง risk log (ต้องยืนยันก่อน)", obj(schema{
		"project": featureProject, "type": schema{"type": "string", "enum": []string{"risk", "decision"}}, "title": str("หัวข้อสั้น"),
		"detail": str(""), "impact": level, "likelihood": level, "owner": str("ผู้ดูแล เว้น = PM"),
		"mitigation": str("แผนรองรับ"), "source": str("ที่มา เช่น ประชุม 21 ต.ค."),
	}, "type", "title")),
	tool("list_risks", "ความเสี่ยงที่เปิดอยู่ การตัดสินใจล่าสุด และ change request ของโปรเจกต์", obj(schema{"project": featureProject, "include_closed": boolean})),
	tool("update_record", "เปลี่ยนสถานะรายการใน risk log / CR / งวดงาน ตามรหัส เช่น R-07 ปิดแล้ว, CR-03 อนุมัติ, M-02 ส่งมอบแล้ว (ต้องยืนยันก่อน)", obj(schema{
		"code": str(""), "status": schema{"type": "string", "enum": []string{"open", "closed", "pending", "approved", "rejected", "done"}},
		"note": str(""), "milestone": str("สำหรับ CR: ย้ายไปงวดอื่น (รหัสหรือชื่องวด)"),
	}, "code")),
	tool("log_change_request", "บันทึก change request พร้อมประเมินผลกระทบต่องวดงาน (ต้องยืนยันก่อน). ถ้าผู้ใช้ไม่ให้ตัวเลขประเมิน ให้ประเมินคร่าว ๆ จากขอบเขตงานเองและบอกว่าเป็นค่าประมาณ", obj(schema{
		"project": featureProject, "title": str(""), "detail": str(""), "requester": str(""),
		"requested_on": str("YYYY-MM-DD"), "tasks": schema{"type": "integer"}, "man_days": schema{"type": "number"},
		"milestone":       str("งวดที่จะใส่ (รหัส M-xx หรือชื่องวด) เว้น = งวดถัดไป"),
		"create_document": schema{"type": "boolean", "description": "สร้างเอกสาร CR ใน Notion ด้วย (ค่าเริ่มต้น true ถ้าเชื่อม Notion แล้ว)"},
	}, "title")),
	tool("set_milestone", "เพิ่มหรือแก้งวดงานตามสัญญา: วันส่งมอบ มูลค่างวด module ในงวด และรายการส่งมอบตาม TOR (ต้องยืนยันก่อน)", obj(schema{
		"project": featureProject, "code": str("รหัสเดิม M-xx ถ้าจะแก้"), "name": str("เช่น งวดที่ 2"),
		"due": str("YYYY-MM-DD"), "value_percent": schema{"type": "number"},
		"modules":      strArray,
		"deliverables": schema{"type": "array", "items": obj(schema{"tor": str("ข้อใน TOR เช่น 4.2"), "name": str(""), "item_key": str("")}, "name")},
	}, "name", "due")),
	tool("list_milestones", "งวดงานของโปรเจกต์ พร้อมความคืบหน้า module และสถานะรายการส่งมอบตาม TOR (ครบ/กำลังทำ/ยังไม่มีงาน)", obj(schema{"project": featureProject, "include_done": boolean})),
	tool("set_reminder", "ตั้งเตือนความจำ บอทจะส่งข้อความเตือนตามเวลา (ไม่ต้องยืนยัน)", obj(schema{"at": str("เวลาท้องถิ่น YYYY-MM-DDTHH:MM"), "text": str(""), "item_key": str("")}, "at", "text")),
	tool("list_reminders", "การเตือนที่ตั้งไว้", obj(schema{})),
	tool("cancel_reminder", "ยกเลิกการเตือนตามหมายเลข", obj(schema{"id": schema{"type": "integer"}}, "id")),
	tool("set_budget", "ตั้งงบ man-day ของโปรเจกต์ (รวม และราย module) (ต้องยืนยันก่อน)", obj(schema{
		"project": featureProject, "total_man_days": schema{"type": "number"},
		"modules": schema{"type": "object", "additionalProperties": schema{"type": "number"}, "description": `{"ชื่อ module": man-day}`},
	}, "total_man_days")),
	tool("log_effort", "บันทึกเวลาที่ใช้ทำงาน (ชั่วโมง) ใช้คำนวณ man-day เทียบงบ (ต้องยืนยันก่อน)", obj(schema{
		"entries": schema{"type": "array", "minItems": 1, "maxItems": 20,
			"items": obj(schema{"who": str(""), "item_key": str(""), "project": featureProject, "hours": schema{"type": "number"}, "date": str(""), "note": str("")}, "hours")},
	}, "entries")),
	tool("budget_status", "man-day ที่ใช้ไปเทียบงบ ความคืบหน้า และคาดการณ์ว่าจะเกินงบเท่าไร", obj(schema{"project": featureProject})),
	tool("team_capacity", "กำลังคนของทีมรายสัปดาห์: งานที่ครบกำหนดต่อคนเทียบวันทำงานหลังหักวันลา บอกว่าใครงานล้น", obj(schema{"project": featureProject, "week_start": str("วันจันทร์ YYYY-MM-DD เว้น = สัปดาห์หน้า")})),
	tool("find_meeting_slots", "หาช่วงเวลาที่ผู้เข้าร่วมว่างจาก Google Calendar (เวลาทำงาน จ.-ศ.) ระบบจะส่งการ์ดให้ผู้ใช้เลือกช่วงเวลาเอง", obj(schema{
		"project": featureProject, "attendees": schema{"type": "array", "items": schema{"type": "string"}, "description": "ชื่อเล่น ชื่อ หรืออีเมล เว้น = สมาชิกทุกคนในโปรเจกต์"},
		"date_from": str("YYYY-MM-DD"), "days": schema{"type": "integer", "minimum": 1, "maximum": 10}, "duration_minutes": schema{"type": "integer"}, "title": str(""),
	}, "date_from", "duration_minutes")),
	tool("schedule_meeting", "นัดประชุมใน Google Calendar พร้อมลิงก์ Google Meet และส่ง invite (ต้องยืนยันก่อน)", obj(schema{
		"project": featureProject, "title": str(""), "start": str("YYYY-MM-DDTHH:MM"), "end": str("YYYY-MM-DDTHH:MM"),
		"attendees": strArray, "agenda": str(""),
	}, "title", "start", "end")),
	tool("search_email", "ค้นอีเมลใน Gmail ของผู้ใช้ (อ่านอย่างเดียว ได้หัวเรื่อง ผู้ส่ง และข้อความตัวอย่าง) query ใช้รูปแบบค้นหาของ Gmail เช่น from:client.com newer_than:3d หรือ is:unread",
		obj(schema{"query": str(""), "max": schema{"type": "integer", "minimum": 1, "maximum": 20}}, "query")),
	tool("list_meetings", "ประชุมใน Google Calendar ช่วงวันที่ พร้อมผู้เข้าร่วม (ใช้หาอีเมลผู้เข้าประชุมเพื่อส่ง minute)", obj(schema{"date_from": str(""), "date_to": str(""), "query": str("")}, "date_from")),
	tool("send_email", `ส่งอีเมลจาก Gmail บริษัทของผู้ใช้ เช่น minute ประชุม รายงาน แจ้งลูกค้า (ต้องยืนยันก่อน). body เขียนแบบข้อความธรรมดา ขึ้นบรรทัดใหม่ได้ ใช้ "- " สำหรับรายการ`, obj(schema{
		"to": schema{"type": "array", "items": schema{"type": "string"}, "minItems": 1}, "cc": strArray,
		"subject": str(""), "body": str(""), "project": featureProject,
		"notion_page_id": str("ถ้าเป็น minute จาก Notion ใส่ id หน้า เพื่อบันทึกกลับว่าส่งแล้ว"),
	}, "to", "subject", "body")),
	tool("meeting_prep", "สรุปก่อนประชุม: ความคืบหน้า action items ครั้งก่อน ความเสี่ยงและ CR ที่ควรคุย", obj(schema{"query": str("ชื่อประชุม เว้น = ประชุมถัดไปวันนี้")})),
	tool("notion_search", "ค้นหน้าใน Notion (จดประชุม ข้อตกลง เอกสาร) คืนชื่อ ลิงก์ และเนื้อหาของหน้าที่ตรงที่สุด ใช้ตอบคำถามว่าเคยตกลงอะไรไว้ ให้อ้างชื่อหน้าและวันที่", obj(schema{"query": str("")}, "query")),
	tool("notion_read_page", "อ่านเนื้อหาหน้า Notion ตาม id", obj(schema{"id": str("")}, "id")),
	tool("save_to_notion", "สร้างหน้าใหม่ใน Notion (ใต้หน้าที่ตั้งไว้ในการตั้งค่า) หรือเพิ่มเนื้อหาต่อท้ายหน้าเดิม (ต้องยืนยันก่อน). content ใช้ # หัวข้อ, - รายการ, [ ] งาน",
		obj(schema{"title": str(""), "content": str(""), "append_to_page_id": str("")}, "title", "content")),
	tool("weekly_report", "ร่างรายงานประจำสัปดาห์จากข้อมูลจริง ระบบจะส่งการ์ดร่างพร้อมปุ่มส่งอีเมลให้เอง", obj(schema{})),
	tool("retro_draft", "ร่างสรุป retrospective ของ sprint (cycle) ที่จบล่าสุด", obj(schema{"project": featureProject})),
	tool("nudge_stale", "ทวงอัปเดตงานที่นิ่งนาน โดยคอมเมนต์ในงาน (ต้องยืนยันก่อน). เว้น keys = งานที่ค้างนานทั้งหมดของโปรเจกต์",
		obj(schema{"project": featureProject, "keys": strArray, "message": str("")})),
	tool("settings_link", "ลิงก์หน้าตั้งค่าบนเว็บ (การเชื่อมต่อ เปลี่ยนบัญชี ชื่อเล่น การแจ้งเตือน)", obj(schema{})),
}

var writeTools = []string{"create_work_items", "update_work_item", "create_cycle", "set_module_dates", "create_project", "add_project_members", "add_comment",
	"log_risk", "update_record", "log_change_request", "set_milestone", "set_budget", "log_effort", "schedule_meeting", "send_email", "save_to_notion", "nudge_stale"}

func (a *App) initTools() {
	a.tools = append(append([]port.ToolDef{}, coreTools...), featureTools...)
	a.toolNames = map[string]bool{}
	for _, t := range a.tools {
		a.toolNames[t.Name] = true
	}
	a.writes = map[string]bool{}
	for _, n := range writeTools {
		a.writes[n] = true
	}
}

// ToolDefs lists every tool the agent can call.
func (a *App) ToolDefs() []port.ToolDef { return a.tools }

// IsWriteTool reports tools that need the owner's confirmation.
func (a *App) IsWriteTool(name string) bool { return a.writes[name] }
