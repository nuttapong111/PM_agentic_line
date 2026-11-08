// Package port declares the interfaces the use cases depend on. Adapters implement them.
package port

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
)

type freshKey struct{}

// WithFresh marks a request that must not be answered from an adapter's short-lived cache.
func WithFresh(ctx context.Context) context.Context { return context.WithValue(ctx, freshKey{}, true) }

// IsFresh reports a context marked by WithFresh.
func IsFresh(ctx context.Context) bool { v, _ := ctx.Value(freshKey{}).(bool); return v }

// Message is one chat message in the messenger's own JSON shape (LINE Messaging API).
type Message = map[string]any

// ---------------------------------------------------------------- task tool

// Task provider kinds.
const (
	KindPlane   = "plane"
	KindBuiltin = "builtin"
	KindNone    = "none"
)

// TaskProvider is the task tool (Plane today, the built-in list as fallback).
// Adding Jira, ClickUp or Linear means one new adapter implementing this.
type TaskProvider interface {
	Kind() string
	// WorkspaceKey identifies the data source; stored history is kept per workspace.
	WorkspaceKey() string
	Me(ctx context.Context) (domain.Member, error)
	// AllProjects ignores the follow list (for the Settings picker).
	AllProjects(ctx context.Context) ([]domain.Project, error)
	Projects(ctx context.Context) ([]domain.Project, error)
	States(ctx context.Context, projectID string) ([]domain.State, error)
	Members(ctx context.Context, projectID string) ([]domain.Member, error)
	Labels(ctx context.Context, projectID string) ([]domain.Label, error)
	Cycles(ctx context.Context, projectID string) ([]domain.Cycle, error)
	Modules(ctx context.Context, projectID string) ([]domain.Module, error)
	CycleLinks(ctx context.Context, projectID, cycleID string) ([]domain.CycleLink, error)
	ModuleItemIDs(ctx context.Context, projectID, moduleID string) ([]string, error)
	WorkItems(ctx context.Context, p domain.Project) ([]domain.WorkItem, error)
	WorkItemByKey(ctx context.Context, key string) (domain.WorkItem, error)
	CreateWorkItem(ctx context.Context, p domain.Project, body domain.WorkItemWrite) (domain.WorkItem, error)
	UpdateWorkItem(ctx context.Context, p domain.Project, id string, body domain.WorkItemWrite) (domain.WorkItem, error)
	NewProject(ctx context.Context, in domain.ProjectWrite) (domain.Project, error)
	WorkspaceMembers(ctx context.Context) ([]domain.Member, error)
	AddProjectMember(ctx context.Context, projectID, memberID string, role int) error
	// MembersURL is the project's member settings page, for adding people by hand.
	MembersURL(p domain.Project) string
	CreateCycle(ctx context.Context, projectID string, c domain.CycleWrite) (domain.Cycle, error)
	AddToCycle(ctx context.Context, projectID, cycleID string, itemIDs []string) error
	AddToModule(ctx context.Context, projectID, moduleID string, itemIDs []string) error
	AddComment(ctx context.Context, p domain.Project, itemID, html string) error
	ItemURL(it domain.WorkItem) string
	KeyURL(key string) string
	ProjectURL(p domain.Project) string
}

// BuiltinAdmin manages the built-in task list's own tables.
type BuiltinAdmin interface {
	Migrate(ctx context.Context) error
	CreateProject(ctx context.Context, name, identifier string) (domain.Project, error)
}

// ---------------------------------------------------------------- persistence

// StatePersister keeps the owner state document. Load returns nil when nothing is saved yet.
type StatePersister interface {
	Load(ctx context.Context) ([]byte, error)
	Save(ctx context.Context, doc []byte) error
}

// RecordRepo stores risks, decisions, CRs, milestones, reminders, budgets and effort, per workspace.
type RecordRepo interface {
	Enabled() bool
	Add(ctx context.Context, ws string, kind domain.RecordKind, projectID *string, data any, status string, dueAt *time.Time) (domain.Record, error)
	List(ctx context.Context, ws string, kinds []domain.RecordKind, f domain.ListFilter) ([]domain.Record, error)
	Get(ctx context.Context, ws string, id int64) (*domain.Record, error)
	GetByCode(ctx context.Context, ws, code string) (*domain.Record, error)
	Update(ctx context.Context, ws string, id int64, p domain.RecordPatch) (*domain.Record, error)
	// DueReminders returns open reminders whose time has come, in any workspace.
	DueReminders(ctx context.Context) ([]domain.Record, error)
}

type HistoryPoint struct {
	Day          string  `json:"day"`
	Open         int     `json:"open"`
	Overdue      int     `json:"overdue"`
	Blocked      int     `json:"blocked"`
	Stale        int     `json:"stale"`
	DoneThisWeek int     `json:"doneThisWeek"`
	Unassigned   int     `json:"unassigned"`
	UrgentBugs   int     `json:"urgentBugs"`
	CycleName    *string `json:"cycleName"`
	CyclePercent *int    `json:"cyclePercent"`
}

type ActivityRow struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Summary string    `json:"summary"`
}

type UsageRow struct {
	Month     string `json:"month"`
	Calls     int    `json:"calls"`
	Input     int64  `json:"input"`
	Output    int64  `json:"output"`
	CacheRead int64  `json:"cacheRead"`
}

type HistoryResult struct {
	Enabled  bool           `json:"enabled"`
	Points   []HistoryPoint `json:"points"`
	Activity []ActivityRow  `json:"activity"`
	Usage    []UsageRow     `json:"usage"`
}

type WorkspaceSummary struct {
	WS         string  `json:"ws"`
	Since      *string `json:"since"`
	Points     int     `json:"points"`
	Activity   int     `json:"activity"`
	Records    int     `json:"records"`
	Milestones int     `json:"milestones"`
}

// Table is exported rows with their column order (for CSV).
type Table struct {
	Columns []string
	Rows    []map[string]any
}

// MarshalJSON writes the rows as an array of objects.
func (t Table) MarshalJSON() ([]byte, error) {
	if t.Rows == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(t.Rows)
}

type ExportData struct {
	Metrics  Table `json:"metrics"`
	Activity Table `json:"activity"`
	Records  Table `json:"records"`
}

// HistoryRepo keeps the activity log, token usage and daily metric snapshots.
type HistoryRepo interface {
	Enabled() bool
	LogActivity(ctx context.Context, ws, kind, summary string, projectID *string, detail any)
	LogUsage(ctx context.Context, model string, input, output, cacheRead int64)
	Snapshot(ctx context.Context, ws string, m domain.ProjectMetrics) error
	History(ctx context.Context, projectID string, days int) (HistoryResult, error)
	// MetricsAround returns the newest point at least daysAgo days old.
	MetricsAround(ctx context.Context, projectID string, daysAgo int) (*HistoryPoint, error)
	DataSummary(ctx context.Context) ([]WorkspaceSummary, error)
	Export(ctx context.Context, ws string) (ExportData, error)
	// DeleteWorkspace deletes one workspace's rows, or everything (incl. state and built-in tables) when ws is nil.
	DeleteWorkspace(ctx context.Context, ws *string) error
	UsageThisMonth(ctx context.Context) (*int, error)
	// BackfillWorkspace tags rows written before workspaces were tracked.
	BackfillWorkspace(ctx context.Context, ws string) error
	// SaveDashboard keeps the latest dashboard metrics of a project, so a restart serves them at once.
	SaveDashboard(ctx context.Context, ws string, m domain.ProjectMetrics, at time.Time) error
	// LoadDashboard returns the saved dashboard metrics of a workspace, by project ID.
	LoadDashboard(ctx context.Context, ws string) (map[string]DashboardPoint, error)
}

// DashboardPoint is saved dashboard metrics and when they were computed.
type DashboardPoint struct {
	At      time.Time
	Metrics domain.ProjectMetrics
}

// ---------------------------------------------------------------- external services

// Secrets encrypts values saved from the Settings page.
type Secrets interface {
	Encrypt(plain string) string
	Decrypt(blob string) (string, bool)
}

type GoogleTokens struct {
	Refresh   string
	Access    string
	ExpiresIn int
	Email     string
}

type MeetingRequest struct {
	Summary     string
	Description string
	StartISO    string
	EndISO      string
	TimeZone    string
	Attendees   []string
}

type Email struct {
	To, Cc  []string
	Subject string
	HTML    string
}

// GoogleAPI is Google OAuth, Calendar and Gmail. Calls take an access token.
type GoogleAPI interface {
	AuthURL(clientID, redirectURI, state string) string
	ExchangeCode(ctx context.Context, clientID, secret, redirectURI, code string) (GoogleTokens, error)
	RefreshAccess(ctx context.Context, clientID, secret, refresh string) (access string, expiresIn int, err error)
	Revoke(ctx context.Context, refresh string)
	ListEvents(ctx context.Context, token, calendarID string, from, to time.Time, q string) ([]domain.CalEvent, error)
	FreeBusy(ctx context.Context, token string, emails []string, from, to time.Time, tz string) (map[string][]domain.Interval, []string, error)
	InsertMeeting(ctx context.Context, token string, m MeetingRequest) (domain.CalEvent, error)
	SendEmail(ctx context.Context, token string, e Email) (string, error)
	// ListMail returns message headers and snippets for a Gmail search (needs gmail.readonly).
	ListMail(ctx context.Context, token, query string, max int) ([]domain.MailItem, error)
}

// NotionAPI is the Notion REST API. Calls take the integration token.
type NotionAPI interface {
	Whoami(ctx context.Context, token string) (string, error)
	Search(ctx context.Context, token, query string, limit int) ([]domain.NotionPage, error)
	PageText(ctx context.Context, token, id string, maxChars int) (domain.PageText, error)
	CreatePage(ctx context.Context, token, parentID, title, markdown string) (domain.NotionPage, error)
	Append(ctx context.Context, token, pageID, markdown string) error
}

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type LLMRequest struct {
	Model     string
	MaxTokens int
	System    []domain.Block
	Tools     []ToolDef
	Messages  []domain.ChatMessage
	Effort    string
}

type LLMUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
}

type LLMResponse struct {
	Content    []domain.Block `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      LLMUsage       `json:"usage"`
}

// LLM is the Claude Messages API.
type LLM interface {
	Create(ctx context.Context, apiKey string, req LLMRequest) (LLMResponse, error)
	CheckModel(ctx context.Context, apiKey, model string) error
}

// Messenger sends chat messages (LINE).
type Messenger interface {
	Reply(ctx context.Context, replyToken string, msgs []Message) error
	Push(ctx context.Context, to string, msgs []Message) error
	ShowLoading(ctx context.Context, chatID string, seconds int) error
	Content(ctx context.Context, messageID string) ([]byte, error)
}

// Presenter turns view models into messenger messages (Flex cards).
type Presenter interface {
	Text(t string) Message
	Status(m domain.ProjectMetrics, dashboardURL string) Message
	Digest(list []domain.ProjectMetrics, dateLabel, dashboardURL string, meetings, mails []string) Message
	Confirm(p domain.Preview, id string, proactive bool, previewURL string) Message
	Result(title string, links []domain.Link) Message
	ProjectPicker(prompt string, projects []domain.Project) Message
	Info(c domain.InfoCard) Message
	QuickText(t string, items []domain.QuickItem) Message
}
