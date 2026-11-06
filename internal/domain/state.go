package domain

import (
	"encoding/json"
	"regexp"
	"strings"
)

// AppState is the owner's state: conversation, pending confirmations, connections (secrets
// encrypted) and preferences. It is saved as one JSON document; field names are stable because
// the same document is read back after upgrades.
type AppState struct {
	CurrentProjectID *string               `json:"currentProjectId"`
	Conversation     Conversation          `json:"conversation"`
	Pending          *PendingAction        `json:"pending"`
	Proactive        map[string]*Proactive `json:"proactive"`
	// Sent holds dedupe keys for alerts already sent (value = epoch ms).
	Sent        map[string]int64 `json:"sent"`
	OAuth       *OAuthState      `json:"oauth"`
	Connections Connections      `json:"connections"`
	Prefs       Prefs            `json:"prefs"`
	Usage       Usage            `json:"usage"`
}

type Conversation struct {
	Messages []ChatMessage `json:"messages"`
	LastAt   int64         `json:"lastAt"`
}

// Block is one content block of a chat message, kept as raw JSON fields so whatever the model
// API returns is stored and sent back unchanged.
type Block = map[string]any

type ChatMessage struct {
	Role    string  `json:"role"`
	Content []Block `json:"content"`
}

// UnmarshalJSON accepts content given as a plain string too.
func (m *ChatMessage) UnmarshalJSON(b []byte) error {
	var raw struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	m.Role = raw.Role
	var s string
	if json.Unmarshal(raw.Content, &s) == nil {
		m.Content = []Block{{"type": "text", "text": s}}
		return nil
	}
	return json.Unmarshal(raw.Content, &m.Content)
}

// TextBlock builds a text content block.
func TextBlock(t string) Block { return Block{"type": "text", "text": t} }

// PendingWrite is a write tool call waiting for the owner's confirmation.
type PendingWrite struct {
	ToolUseID string          `json:"toolUseId"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Preview   Preview         `json:"preview"`
}

type PendingAction struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"createdAt"`
	// ReadyResults are tool_result blocks already computed for read tools in the same assistant turn.
	ReadyResults []Block        `json:"readyResults"`
	Writes       []PendingWrite `json:"writes"`
}

// Proactive is a confirm card the bot sent on its own (digest, alerts), not from a chat turn.
type Proactive struct {
	ID        string  `json:"id"`
	CreatedAt int64   `json:"createdAt"`
	Name      string  `json:"name"`
	Preview   Preview `json:"preview"`
}

type OAuthState struct {
	State string `json:"state"`
	At    int64  `json:"at"`
}

type Usage struct {
	Month           string `json:"month"`
	InputTokens     int64  `json:"inputTokens"`
	OutputTokens    int64  `json:"outputTokens"`
	CacheReadTokens int64  `json:"cacheReadTokens"`
}

// Task provider choices. "env" means: use the PLANE_* env vars.
const (
	ProviderPlane   = "plane"
	ProviderBuiltin = "builtin"
	ProviderNone    = "none"
	ProviderEnv     = "env"
)

type PlaneAccount struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

type PlaneConnection struct {
	BaseURL    string        `json:"baseUrl"`
	Workspace  string        `json:"workspace"`
	Projects   []string      `json:"projects"`
	TokenEnc   string        `json:"tokenEnc"`
	TokenLast4 string        `json:"tokenLast4"`
	Account    *PlaneAccount `json:"account,omitempty"`
	CheckedAt  *int64        `json:"checkedAt,omitempty"`
	OK         *bool         `json:"ok,omitempty"`
}

type TaskConnection struct {
	Provider string           `json:"provider"`
	Plane    *PlaneConnection `json:"plane,omitempty"`
}

type ClaudeConnection struct {
	KeyEnc  string `json:"keyEnc"`
	Last4   string `json:"last4"`
	SavedAt int64  `json:"savedAt"`
}

type GoogleConnection struct {
	ClientID        string `json:"clientId"`
	ClientSecretEnc string `json:"clientSecretEnc"`
	RefreshEnc      string `json:"refreshEnc,omitempty"`
	Email           string `json:"email,omitempty"`
	ConnectedAt     int64  `json:"connectedAt,omitempty"`
}

type NotionConnection struct {
	TokenEnc string `json:"tokenEnc"`
	Last4    string `json:"last4"`
	BotName  string `json:"botName,omitempty"`
	SavedAt  int64  `json:"savedAt"`
}

type Connections struct {
	Task   TaskConnection    `json:"task"`
	Claude *ClaudeConnection `json:"claude,omitempty"`
	Google *GoogleConnection `json:"google,omitempty"`
	Notion *NotionConnection `json:"notion,omitempty"`
}

type WeeklyPrefs struct {
	Enabled bool     `json:"enabled"`
	Day     int      `json:"day"`
	Time    string   `json:"time"`
	To      []string `json:"to"`
	Cc      []string `json:"cc"`
}

type NudgePrefs struct {
	Enabled bool   `json:"enabled"`
	Day     int    `json:"day"`
	Time    string `json:"time"`
}

type Prefs struct {
	Nicknames          map[string]string `json:"nicknames"`
	DigestTime         string            `json:"digestTime"`
	WorkdaysOnly       bool              `json:"workdaysOnly"`
	QuietFrom          string            `json:"quietFrom"`
	QuietTo            string            `json:"quietTo"`
	StaleDays          int               `json:"staleDays"`
	BugLabel           string            `json:"bugLabel"`
	Weekly             WeeklyPrefs       `json:"weekly"`
	MeetingPrepMinutes int               `json:"meetingPrepMinutes"`
	MilestoneAlertDays []int             `json:"milestoneAlertDays"`
	Nudge              NudgePrefs        `json:"nudge"`
	LeaveCalendarID    string            `json:"leaveCalendarId"`
	NotionParentPageID string            `json:"notionParentPageId"`
	ManDayHours        float64           `json:"manDayHours"`
}

// PrefDefaults are the env-derived starting values.
type PrefDefaults struct {
	Nicknames  map[string]string
	DigestCron string
	StaleDays  int
	BugLabel   string
}

var digits = regexp.MustCompile(`^\d+$`)

// DigestTimeFromCron reads "30 8 * * 1-5" as "08:30".
func DigestTimeFromCron(c string) string {
	f := strings.Fields(c)
	pad := func(x string, def string) string {
		if x == "" {
			x = def
		}
		if !digits.MatchString(x) {
			return "00"
		}
		if len(x) == 1 {
			return "0" + x
		}
		return x
	}
	var m, h string
	if len(f) > 0 {
		m = f[0]
	}
	if len(f) > 1 {
		h = f[1]
	}
	return pad(h, "8") + ":" + pad(m, "30")
}

// DefaultPrefs builds the preferences used before the owner changes anything.
func DefaultPrefs(d PrefDefaults) Prefs {
	nick := d.Nicknames
	if nick == nil {
		nick = map[string]string{}
	}
	return Prefs{
		Nicknames: nick, DigestTime: DigestTimeFromCron(d.DigestCron), WorkdaysOnly: true,
		QuietFrom: "20:00", QuietTo: "08:00", StaleDays: d.StaleDays, BugLabel: d.BugLabel,
		Weekly:             WeeklyPrefs{Enabled: true, Day: 5, Time: "16:00", To: []string{}, Cc: []string{}},
		MeetingPrepMinutes: 15, MilestoneAlertDays: []int{14, 7, 3},
		Nudge:       NudgePrefs{Enabled: true, Day: 4, Time: "09:00"},
		ManDayHours: 8,
	}
}

// EmptyState is a fresh state.
func EmptyState(d PrefDefaults) *AppState {
	return &AppState{
		Conversation: Conversation{Messages: []ChatMessage{}},
		Proactive:    map[string]*Proactive{},
		Sent:         map[string]int64{},
		Connections:  Connections{Task: TaskConnection{Provider: ProviderEnv}},
		Prefs:        DefaultPrefs(d),
	}
}

// UpgradeState reads saved state, filling fields added in newer versions so older saves keep working.
// Defaults are decoded first and the saved document on top, so missing keys keep their default.
func UpgradeState(saved []byte, d PrefDefaults) (*AppState, error) {
	s := EmptyState(d)
	// A saved nickname map replaces the default one (decoding into a non-nil map would merge).
	var peek struct {
		Prefs struct {
			Nicknames json.RawMessage `json:"nicknames"`
		} `json:"prefs"`
	}
	if json.Unmarshal(saved, &peek) == nil && len(peek.Prefs.Nicknames) > 0 {
		s.Prefs.Nicknames = nil
	}
	if err := json.Unmarshal(saved, s); err != nil {
		return nil, err
	}
	if s.Connections.Task.Provider == "" {
		s.Connections.Task.Provider = ProviderEnv
	}
	if s.Proactive == nil {
		s.Proactive = map[string]*Proactive{}
	}
	if s.Sent == nil {
		s.Sent = map[string]int64{}
	}
	if s.Conversation.Messages == nil {
		s.Conversation.Messages = []ChatMessage{}
	}
	if s.Prefs.Nicknames == nil {
		s.Prefs.Nicknames = map[string]string{}
	}
	if s.Prefs.Weekly.To == nil {
		s.Prefs.Weekly.To = []string{}
	}
	if s.Prefs.Weekly.Cc == nil {
		s.Prefs.Weekly.Cc = []string{}
	}
	if s.Prefs.MilestoneAlertDays == nil {
		s.Prefs.MilestoneAlertDays = []int{}
	}
	return s, nil
}

// Prune drops bookkeeping older than 14 days so the saved state stays small.
func (s *AppState) Prune(nowMs int64) {
	cutoff := nowMs - 14*86_400_000
	for k, v := range s.Sent {
		if v < cutoff {
			delete(s.Sent, k)
		}
	}
	for k, v := range s.Proactive {
		if v == nil || v.CreatedAt < cutoff {
			delete(s.Proactive, k)
		}
	}
}

// ResetConversation forgets the chat and any pending card.
func (s *AppState) ResetConversation() {
	s.Conversation = Conversation{Messages: []ChatMessage{}}
	s.Pending = nil
}

// Current returns the current project id or "".
func (s *AppState) Current() string { return deref(s.CurrentProjectID) }
