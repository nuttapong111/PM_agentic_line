package domain

import (
	"encoding/json"
	"time"
)

// Records are what the bot keeps that the task tool doesn't: risks, decisions, change requests,
// payment milestones (with TOR deliverables), reminders, man-day budgets and effort logs.

type RecordKind string

const (
	KindRisk      RecordKind = "risk"
	KindDecision  RecordKind = "decision"
	KindCR        RecordKind = "cr"
	KindMilestone RecordKind = "milestone"
	KindReminder  RecordKind = "reminder"
	KindBudget    RecordKind = "budget"
	KindEffort    RecordKind = "effort"
)

// RecordPrefix gives kinds that get a human code (R-01, D-01, CR-01, M-01).
var RecordPrefix = map[RecordKind]string{KindRisk: "R", KindDecision: "D", KindCR: "CR", KindMilestone: "M"}

type Level string

const (
	High   Level = "high"
	Medium Level = "medium"
	Low    Level = "low"
)

// LevelTH is the Thai label of a level.
var LevelTH = map[Level]string{High: "สูง", Medium: "กลาง", Low: "ต่ำ"}

// LevelOf reads a level, falling back to medium.
func LevelOf(v string) Level {
	switch Level(v) {
	case High, Medium, Low:
		return Level(v)
	}
	return Medium
}

// LevelOrder sorts high first.
var LevelOrder = map[Level]int{High: 0, Medium: 1, Low: 2}

// Record is one row of pm_records. JSON field names match the table so the dashboard reads rows as-is.
type Record struct {
	ID        int64           `json:"id"`
	WS        string          `json:"ws"`
	Kind      RecordKind      `json:"kind"`
	Code      *string         `json:"code"`
	ProjectID *string         `json:"project_id"`
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
	DueAt     *time.Time      `json:"due_at"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// CodeOr returns the code or a fallback.
func (r Record) CodeOr(fallback string) string {
	if r.Code == nil {
		return fallback
	}
	return *r.Code
}

// Decode unmarshals the data column into v.
func (r Record) Decode(v any) { _ = json.Unmarshal(r.Data, v) }

type RiskData struct {
	Title      string `json:"title"`
	Detail     string `json:"detail,omitempty"`
	Impact     Level  `json:"impact"`
	Likelihood Level  `json:"likelihood"`
	Owner      string `json:"owner,omitempty"`
	Mitigation string `json:"mitigation,omitempty"`
}

type DecisionData struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Source string `json:"source,omitempty"`
	Date   string `json:"date"`
}

type CrData struct {
	Title       string   `json:"title"`
	Detail      string   `json:"detail,omitempty"`
	Requester   string   `json:"requester,omitempty"`
	RequestedOn string   `json:"requestedOn,omitempty"`
	Tasks       *int     `json:"tasks,omitempty"`
	ManDays     *float64 `json:"manDays,omitempty"`
	MilestoneID *int64   `json:"milestoneId"`
	ImpactNote  string   `json:"impactNote"`
	NotionURL   string   `json:"notionUrl,omitempty"`
}

type Deliverable struct {
	Tor     string `json:"tor,omitempty"`
	Name    string `json:"name"`
	ItemKey string `json:"itemKey,omitempty"`
}

type MilestoneData struct {
	Name         string        `json:"name"`
	Due          string        `json:"due"`
	ValuePercent *float64      `json:"valuePercent,omitempty"`
	Modules      []string      `json:"modules"`
	Deliverables []Deliverable `json:"deliverables"`
}

type ReminderData struct {
	Text    string `json:"text"`
	ItemKey string `json:"itemKey,omitempty"`
	At      string `json:"at"`
}

type BudgetData struct {
	TotalManDays float64            `json:"totalManDays"`
	Modules      map[string]float64 `json:"modules"`
}

type EffortData struct {
	Who     string  `json:"who,omitempty"`
	ItemKey string  `json:"itemKey,omitempty"`
	Hours   float64 `json:"hours"`
	Date    string  `json:"date"`
	Note    string  `json:"note,omitempty"`
}

// TitleOrName is the label of a record: its title, or its name for milestones.
func (r Record) TitleOrName() string {
	var v struct {
		Title string `json:"title"`
		Name  string `json:"name"`
	}
	r.Decode(&v)
	if v.Title != "" {
		return v.Title
	}
	return v.Name
}

var crStatusTH = map[string]string{"pending": "รออนุมัติ", "approved": "อนุมัติแล้ว", "rejected": "ไม่อนุมัติ", "done": "ทำแล้ว", "open": "เปิด", "closed": "ปิด"}

// CrStatusTH is the Thai label of a record status.
func CrStatusTH(s string) string {
	if t, ok := crStatusTH[s]; ok {
		return t
	}
	return s
}

// ListFilter narrows a record listing.
type ListFilter struct {
	ProjectID string   // "" = any; otherwise rows of this project or with no project
	Status    []string // nil = any
	Limit     int      // 0 = 100
}

// RecordPatch changes a record. Data is merged into the existing jsonb.
type RecordPatch struct {
	Status   *string
	Data     map[string]any
	DueAt    *time.Time
	SetDueAt bool
}
