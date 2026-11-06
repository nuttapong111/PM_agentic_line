package domain

import "encoding/json"

// Preview is what a confirm card shows before a write runs. Everything is already resolved to IDs.
type Preview struct {
	Kicker      string `json:"kicker,omitempty"`
	CancelLabel string `json:"cancelLabel,omitempty"`
	// FullHTML is shown on the "ดูฉบับเต็ม" page (emails, reports).
	FullHTML     string        `json:"fullHtml,omitempty"`
	Title        string        `json:"title"`
	Subtitle     string        `json:"subtitle"`
	Lines        []PreviewLine `json:"lines"`
	Warnings     []string      `json:"warnings"`
	ConfirmLabel string        `json:"confirmLabel"`
	Plan         WritePlan     `json:"plan"`
}

type PreviewLine struct {
	Tag  string `json:"tag,omitempty"`
	Text string `json:"text"`
	Sub  string `json:"sub"`
	Warn bool   `json:"warn,omitempty"`
}

// Plan kinds.
const (
	PlanCreate       = "create"
	PlanUpdate       = "update"
	PlanComment      = "comment"
	PlanRecordAdd    = "record_add"
	PlanRecordUpdate = "record_update"
	PlanEffort       = "effort"
	PlanMeeting      = "meeting"
	PlanEmail        = "email"
	PlanNotionPage   = "notion_page"
	PlanComments     = "comments"
	PlanCycle        = "cycle"
	PlanProject      = "project"
	PlanMembers      = "members"
)

// WritePlan is what runs after the owner confirms. Which fields are used depends on Kind.
// It is one flat struct so it round-trips through the saved state unchanged.
type WritePlan struct {
	Kind      string  `json:"kind"`
	ProjectID *string `json:"projectId,omitempty"`

	// create / update / comment
	CycleID  *string         `json:"cycleId,omitempty"`
	ModuleID *string         `json:"moduleId,omitempty"`
	Cycle    *CycleWrite     `json:"cycle,omitempty"`
	ItemIDs  []string        `json:"itemIds,omitempty"`
	Project  *ProjectWrite   `json:"project,omitempty"`
	Members  []PlanMember    `json:"members,omitempty"`
	Items    json.RawMessage `json:"items,omitempty"` // []PlanItem (create) or []PlanTarget (comments)
	ItemID   string          `json:"itemId,omitempty"`
	Key      string          `json:"key,omitempty"`
	Body     *WorkItemWrite  `json:"body,omitempty"`
	HTML     string          `json:"html,omitempty"`

	// record_add / record_update
	RecKind   RecordKind      `json:"recKind,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Status    string          `json:"status,omitempty"`
	ReplaceID int64           `json:"replaceId,omitempty"`
	NotionDoc *NotionDoc      `json:"notionDoc,omitempty"`
	ID        int64           `json:"id,omitempty"`
	Code      string          `json:"code,omitempty"`

	// effort
	Entries []EffortEntry `json:"entries,omitempty"`

	// meeting
	Summary     string   `json:"summary,omitempty"`
	Description string   `json:"description,omitempty"`
	StartISO    string   `json:"startIso,omitempty"`
	EndISO      string   `json:"endIso,omitempty"`
	Attendees   []string `json:"attendees,omitempty"`

	// email
	To           []string `json:"to,omitempty"`
	Cc           []string `json:"cc,omitempty"`
	Subject      string   `json:"subject,omitempty"`
	NotionPageID string   `json:"notionPageId,omitempty"`

	// notion_page
	Title    string `json:"title,omitempty"`
	Markdown string `json:"markdown,omitempty"`
	AppendTo string `json:"appendTo,omitempty"`
}

// PlanItem is one work item to create; ParentIndex points at an earlier item in the same plan.
type PlanItem struct {
	WorkItemWrite
	ParentIndex *int `json:"parentIndex,omitempty"`
}

// PlanTarget is a work item a comment goes to.
type PlanTarget struct {
	ItemID string `json:"itemId"`
	Key    string `json:"key"`
}

type NotionDoc struct {
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
}

type EffortEntry struct {
	ProjectID string     `json:"projectId"`
	Data      EffortData `json:"data"`
}

// CreateItems decodes the items of a create plan.
func (p WritePlan) CreateItems() []PlanItem {
	var out []PlanItem
	_ = json.Unmarshal(p.Items, &out)
	return out
}

// Targets decodes the items of a comments plan.
func (p WritePlan) Targets() []PlanTarget {
	var out []PlanTarget
	_ = json.Unmarshal(p.Items, &out)
	return out
}

// MustJSON marshals v, which must be marshalable.
func MustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// MergePreviews combines several write previews into one confirm card.
func MergePreviews(ps []Preview) Preview {
	if len(ps) == 1 {
		return ps[0]
	}
	out := Preview{Lines: []PreviewLine{}, Warnings: []string{}, Plan: ps[0].Plan}
	titles := make([]string, 0, len(ps))
	for _, p := range ps {
		if out.FullHTML == "" {
			out.FullHTML = p.FullHTML
		}
		titles = append(titles, p.Title)
		out.Lines = append(out.Lines, p.Lines...)
		out.Warnings = append(out.Warnings, p.Warnings...)
	}
	out.Title = itoa(len(ps)) + " รายการรอยืนยัน"
	out.Subtitle = Truncate(joinStr(titles, " · "), 120)
	out.ConfirmLabel = "ยืนยันทั้ง " + itoa(len(ps)) + " รายการ"
	return out
}

// PlanMember is a person to add to a project.
type PlanMember struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role int    `json:"role"`
}
