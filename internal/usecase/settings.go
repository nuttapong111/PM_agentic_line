package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/config"
	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

// Settings page use cases: connections (Plane / built-in, Claude, Google, Notion), followed
// projects, nicknames, notification times, data export and delete. Secrets are stored encrypted
// and never sent back to the browser (only the last 4 characters).

func last4(s string) string {
	r := []rune(s)
	if len(r) <= 4 {
		return s
	}
	return string(r[len(r)-4:])
}

func hostOf(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return u
	}
	return p.Host
}

var schemeRe = regexp.MustCompile(`(?i)^https?://`)

// CleanURL trims a server URL and adds https:// when no scheme is given.
func CleanURL(u string) string {
	s := strings.TrimRight(strings.TrimSpace(u), "/")
	if s == "" || schemeRe.MatchString(s) {
		return s
	}
	return "https://" + s
}

// planeToken is the token for a Plane test/save: a new one, or the saved one only when server + workspace are unchanged.
func (a *App) planeToken(baseURL, ws, token string) (string, error) {
	if t := strings.TrimSpace(token); t != "" {
		return t, nil
	}
	if saved := a.Store.Connections().Task.Plane; saved != nil && saved.BaseURL == baseURL && saved.Workspace == ws {
		if t, ok := a.Secrets.Decrypt(saved.TokenEnc); ok && t != "" {
			return t, nil
		}
	}
	if a.Cfg.EnvPlaneSet() && a.Cfg.PlaneBaseURL == baseURL && a.Cfg.PlaneWorkspace == ws {
		return a.Cfg.PlaneAPIKey, nil
	}
	return "", domain.UserError("เปลี่ยน Server หรือ Workspace ต้องใส่ API token ใหม่ทุกครั้ง (token เดิมจะไม่ถูกส่งไปที่ server ใหม่)")
}

type projectRow struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Identifier string `json:"identifier"`
	Followed   *bool  `json:"followed,omitempty"`
}

type nameEmail struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

// SettingsSnapshot is everything the Settings page shows.
func (a *App) SettingsSnapshot(ctx context.Context) map[string]any {
	s := a.Store.Snapshot()
	t := s.Connections.Task
	p := a.tasks()
	projects, _ := p.AllProjects(ctx)
	fl, _ := p.Projects(ctx)
	followed := map[string]bool{}
	for _, x := range fl {
		followed[x.ID] = true
	}
	members := []nameEmail{}
	seen := map[string]bool{}
	n := 0
	for _, pr := range projects {
		if !followed[pr.ID] || n == 5 {
			continue
		}
		n++
		ms, _ := p.Members(ctx, pr.ID)
		for _, m := range ms {
			if !seen[m.ID] {
				seen[m.ID] = true
				members = append(members, nameEmail{m.Name(), m.Email})
			}
		}
	}
	provider := t.Provider
	if p.Kind() == port.KindPlane && t.Provider == domain.ProviderEnv {
		provider = domain.ProviderEnv
	}
	var plane any
	if t.Plane != nil {
		plane = map[string]any{"baseUrl": t.Plane.BaseURL, "host": hostOf(t.Plane.BaseURL), "workspace": t.Plane.Workspace, "tokenLast4": t.Plane.TokenLast4, "account": t.Plane.Account, "checkedAt": t.Plane.CheckedAt, "ok": t.Plane.OK}
	} else if a.Cfg.EnvPlaneSet() {
		plane = map[string]any{"baseUrl": a.Cfg.PlaneBaseURL, "host": hostOf(a.Cfg.PlaneBaseURL), "workspace": a.Cfg.PlaneWorkspace, "tokenLast4": last4(a.Cfg.PlaneAPIKey), "fromEnv": true}
	}
	claude := map[string]any{"source": "none"}
	if c := s.Connections.Claude; c != nil {
		claude = map[string]any{"source": "settings", "last4": c.Last4, "savedAt": c.SavedAt}
	} else if config.IsSet(a.Cfg.AnthropicAPIKey) {
		claude = map[string]any{"source": "env", "last4": last4(a.Cfg.AnthropicAPIKey)}
	}
	var google, notion any
	if g := s.Connections.Google; g != nil {
		google = map[string]any{"clientId": g.ClientID, "hasSecret": g.ClientSecretEnc != "", "email": g.Email, "connectedAt": g.ConnectedAt, "connected": g.RefreshEnc != ""}
	}
	if nt := s.Connections.Notion; nt != nil {
		notion = map[string]any{"last4": nt.Last4, "botName": nt.BotName, "savedAt": nt.SavedAt}
	}
	calls, _ := a.History.UsageThisMonth(ctx)
	wss, err := a.History.DataSummary(ctx)
	if err != nil || wss == nil {
		wss = []port.WorkspaceSummary{}
	}
	rows := []projectRow{}
	for _, x := range projects {
		f := followed[x.ID]
		rows = append(rows, projectRow{x.ID, x.Name, x.Identifier, &f})
	}
	return map[string]any{
		"db":                a.Records.Enabled(),
		"task":              map[string]any{"provider": provider, "active": p.Kind(), "plane": plane},
		"claude":            claude,
		"model":             a.Cfg.ClaudeModel,
		"calls":             calls,
		"usage":             s.Usage,
		"google":            google,
		"googleRedirectUri": a.Google.RedirectURI(),
		"notion":            notion,
		"line":              map[string]any{"ownerSet": a.Cfg.OwnerUserID != ""},
		"prefs":             s.Prefs,
		"projects":          rows,
		"members":           members,
		"data":              map[string]any{"current": a.ws(), "workspaces": wss},
	}
}

// afterSwitch rebuilds the task provider, fixes the current project and starts a new chat.
func (a *App) afterSwitch() {
	a.Tasks.Reset()
	go func() {
		ps, err := a.tasks().Projects(context.Background())
		if err != nil {
			return
		}
		a.Store.Update(func(s *domain.AppState) {
			cur := s.Current()
			for _, p := range ps {
				if p.ID == cur {
					return
				}
			}
			s.CurrentProjectID = nil
			if len(ps) == 1 {
				s.CurrentProjectID = &ps[0].ID
			}
		})
	}()
	a.Store.Update(func(s *domain.AppState) { s.ResetConversation() })
}

// PlaneInput is what the Settings page sends for a Plane connection.
type PlaneInput struct {
	BaseURL   string   `json:"baseUrl"`
	Workspace string   `json:"workspace"`
	Token     string   `json:"token"`
	Projects  []string `json:"projects"`
}

// PlaneTest checks a connection and lists its projects, saving nothing.
func (a *App) PlaneTest(ctx context.Context, in PlaneInput) (map[string]any, error) {
	baseURL, ws := CleanURL(in.BaseURL), strings.TrimSpace(in.Workspace)
	if baseURL == "" || ws == "" {
		return nil, domain.UserError("ใส่ Server URL และ Workspace slug")
	}
	token, err := a.planeToken(baseURL, ws, in.Token)
	if err != nil {
		return nil, err
	}
	c := a.Tasks.PlaneFrom(baseURL, ws, token, nil)
	me, err := c.Me(ctx)
	if err != nil {
		return nil, err
	}
	ps, err := c.AllProjects(ctx)
	if err != nil {
		return nil, err
	}
	rows := []projectRow{}
	for _, p := range ps {
		rows = append(rows, projectRow{ID: p.ID, Name: p.Name, Identifier: p.Identifier})
	}
	return map[string]any{"ok": true, "account": nameEmail{me.Name(), me.Email}, "projects": rows}, nil
}

// PlaneSave saves a tested Plane connection and follows the given projects.
func (a *App) PlaneSave(ctx context.Context, in PlaneInput) error {
	baseURL, ws := CleanURL(in.BaseURL), strings.TrimSpace(in.Workspace)
	token, err := a.planeToken(baseURL, ws, in.Token)
	if err != nil {
		return err
	}
	me, err := a.Tasks.PlaneFrom(baseURL, ws, token, nil).Me(ctx)
	if err != nil {
		return err
	}
	projects := in.Projects
	if projects == nil {
		projects = []string{}
	}
	now := a.nowMs()
	a.Store.Update(func(s *domain.AppState) {
		s.Connections.Task = domain.TaskConnection{Provider: domain.ProviderPlane, Plane: &domain.PlaneConnection{
			BaseURL: baseURL, Workspace: ws, Projects: projects, TokenEnc: a.Secrets.Encrypt(token), TokenLast4: last4(token),
			Account: &domain.PlaneAccount{Name: me.Name(), Email: me.Email}, CheckedAt: &now, OK: ptr(true),
		}}
	})
	a.afterSwitch()
	return nil
}

// SetFollowed sets which projects the bot follows.
func (a *App) SetFollowed(ids []string) {
	if ids == nil {
		ids = []string{}
	}
	t := a.Store.Connections().Task
	if t.Provider == domain.ProviderBuiltin {
		return // built-in follows all its projects
	}
	if t.Plane == nil && a.Cfg.EnvPlaneSet() {
		// First change from the env setup: keep using the env server/token, now stored here.
		a.Store.Update(func(s *domain.AppState) {
			s.Connections.Task = domain.TaskConnection{Provider: domain.ProviderPlane, Plane: &domain.PlaneConnection{
				BaseURL: a.Cfg.PlaneBaseURL, Workspace: a.Cfg.PlaneWorkspace, Projects: ids,
				TokenEnc: a.Secrets.Encrypt(a.Cfg.PlaneAPIKey), TokenLast4: last4(a.Cfg.PlaneAPIKey),
			}}
		})
	} else if t.Plane != nil {
		a.Store.Update(func(s *domain.AppState) {
			if s.Connections.Task.Plane != nil {
				s.Connections.Task.Plane.Projects = ids
			}
		})
	}
	a.afterSwitch()
}

// PlaneCheck re-checks the current connection.
func (a *App) PlaneCheck(ctx context.Context) (string, error) {
	me, err := a.tasks().Me(ctx)
	now := a.nowMs()
	a.Store.Update(func(s *domain.AppState) {
		if pl := s.Connections.Task.Plane; pl != nil {
			pl.CheckedAt = &now
			pl.OK = ptr(err == nil)
			if err == nil {
				pl.Account = &domain.PlaneAccount{Name: me.Name(), Email: me.Email}
			}
		}
	})
	if err != nil {
		return "", err
	}
	return me.Name(), nil
}

// SetProvider switches to the built-in list or to nothing.
func (a *App) SetProvider(ctx context.Context, want string) error {
	switch want {
	case domain.ProviderBuiltin:
		if !a.Records.Enabled() || a.Builtin == nil {
			return domain.UserError("ระบบในตัวต้องมีฐานข้อมูล PostgreSQL")
		}
		if err := a.Builtin.Migrate(ctx); err != nil {
			return err
		}
		a.Store.Update(func(s *domain.AppState) { s.Connections.Task = domain.TaskConnection{Provider: domain.ProviderBuiltin} })
	case domain.ProviderNone:
		a.Store.Update(func(s *domain.AppState) { s.Connections.Task = domain.TaskConnection{Provider: domain.ProviderNone} })
	default:
		return domain.UserError("provider ไม่ถูกต้อง")
	}
	a.afterSwitch()
	return nil
}

// CreateBuiltinProject adds a project to the built-in list.
func (a *App) CreateBuiltinProject(ctx context.Context, name, identifier string) (domain.Project, error) {
	if a.Builtin == nil {
		return domain.Project{}, domain.UserError("ระบบในตัวต้องมีฐานข้อมูล PostgreSQL")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "โปรเจกต์ใหม่"
	}
	p, err := a.Builtin.CreateProject(ctx, name, identifier)
	if err != nil {
		return p, err
	}
	a.afterSwitch()
	return p, nil
}

// SaveClaudeKey checks a key against the configured model; a new key is saved when it works.
func (a *App) SaveClaudeKey(ctx context.Context, newKey string) (string, error) {
	newKey = strings.TrimSpace(newKey)
	key := newKey
	if key == "" {
		key = a.claudeKey()
	}
	if key == "" {
		return "", domain.UserError("ใช้ key นี้ไม่ได้: ใส่ API key")
	}
	if err := a.LLM.CheckModel(ctx, key, a.Cfg.ClaudeModel); err != nil {
		return "", domain.UserError("ใช้ key นี้ไม่ได้: " + cut(err.Error(), 160))
	}
	if newKey != "" {
		now := a.nowMs()
		a.Store.Update(func(s *domain.AppState) {
			s.Connections.Claude = &domain.ClaudeConnection{KeyEnc: a.Secrets.Encrypt(key), Last4: last4(key), SavedAt: now}
		})
	}
	return a.Cfg.ClaudeModel, nil
}

func (a *App) RemoveClaude() {
	a.Store.Update(func(s *domain.AppState) { s.Connections.Claude = nil })
}

// SaveNotion checks and saves a Notion integration token.
func (a *App) SaveNotion(ctx context.Context, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", domain.UserError("ใส่ Internal integration token")
	}
	name, err := a.Notion.Whoami(ctx, token)
	if err != nil {
		return "", err
	}
	now := a.nowMs()
	a.Store.Update(func(s *domain.AppState) {
		s.Connections.Notion = &domain.NotionConnection{TokenEnc: a.Secrets.Encrypt(token), Last4: last4(token), BotName: name, SavedAt: now}
	})
	return name, nil
}

func (a *App) RemoveNotion() {
	a.Store.Update(func(s *domain.AppState) { s.Connections.Notion = nil })
}

// SaveGoogleClient saves the OAuth client and returns the sign-in URL.
func (a *App) SaveGoogleClient(clientID, secret string) (string, error) {
	clientID, secret = strings.TrimSpace(clientID), strings.TrimSpace(secret)
	cur := a.Store.Connections().Google
	if clientID == "" {
		return "", domain.UserError("ใส่ Client ID")
	}
	same := cur != nil && cur.ClientID == clientID
	if secret == "" && !(same && cur.ClientSecretEnc != "") {
		return "", domain.UserError("ใส่ Client secret")
	}
	a.Store.Update(func(s *domain.AppState) {
		if same {
			g := *cur
			if secret != "" {
				g.ClientSecretEnc = a.Secrets.Encrypt(secret)
			}
			s.Connections.Google = &g
		} else {
			s.Connections.Google = &domain.GoogleConnection{ClientID: clientID, ClientSecretEnc: a.Secrets.Encrypt(secret)}
		}
	})
	return a.Google.AuthURL()
}

// ---------------------------------------------------------------- preferences

var timeRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
var emailSplit = regexp.MustCompile(`[,\s;]+`)
var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
var numSplit = regexp.MustCompile(`[,\s]+`)

var prefKeys = []string{"nicknames", "digestTime", "workdaysOnly", "quietFrom", "quietTo", "staleDays", "bugLabel", "weekly", "meetingPrepMinutes", "milestoneAlertDays", "nudge", "leaveCalendarId", "notionParentPageId", "manDayHours"}

func isTime(raw json.RawMessage) bool {
	var s string
	return json.Unmarshal(raw, &s) == nil && timeRe.MatchString(s)
}

// jsNumber reads a value the way JavaScript's Number() does (NaN → 0).
func jsNumber(raw json.RawMessage) float64 {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case bool:
		if x {
			return 1
		}
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsInf(f, 0) {
			return f
		}
	}
	return 0
}

// jsTruthy reads a value the way JavaScript's !! does.
func jsTruthy(raw json.RawMessage) bool {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	}
	return true
}

func clampF(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func listOf(raw json.RawMessage, split *regexp.Regexp) []string {
	var arr []any
	if json.Unmarshal(raw, &arr) == nil {
		out := make([]string, len(arr))
		for i, x := range arr {
			out[i] = fmt.Sprint(x)
		}
		return out
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		var v any
		_ = json.Unmarshal(raw, &v)
		if v == nil {
			s = ""
		} else {
			s = fmt.Sprint(v)
		}
	}
	return split.Split(s, -1)
}

func emailList(raw json.RawMessage) []string {
	out := []string{}
	if raw == nil {
		return out
	}
	for _, x := range listOf(raw, emailSplit) {
		if x = strings.TrimSpace(x); emailRe.MatchString(x) {
			out = append(out, x)
		}
	}
	return out
}

func timeOr(raw json.RawMessage, def string) string {
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return s
	}
	return def
}

func hasTime(raw json.RawMessage) bool {
	return jsTruthy(raw)
}

// cleanPrefs validates a prefs patch from the Settings page.
func cleanPrefs(body []byte) (map[string]any, error) {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(body, &in); err != nil {
		in = map[string]json.RawMessage{}
	}
	out := map[string]any{}
	for _, k := range prefKeys {
		if v, ok := in[k]; ok {
			out[k] = v
		}
	}
	for _, k := range []string{"digestTime", "quietFrom", "quietTo"} {
		if _, ok := out[k]; ok && !isTime(in[k]) {
			return nil, domain.UserError(k + " ต้องเป็นเวลา HH:MM")
		}
	}
	if raw, ok := in["nicknames"]; ok && jsTruthy(raw) {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		nn := map[string]string{}
		for k, v := range m {
			k2, v2 := strings.TrimSpace(k), strings.TrimSpace(fmt.Sprint(v))
			if k2 != "" && v2 != "" {
				nn[k2] = v2
			}
		}
		out["nicknames"] = nn
	}
	if raw, ok := in["weekly"]; ok && jsTruthy(raw) {
		var w map[string]json.RawMessage
		_ = json.Unmarshal(raw, &w)
		if hasTime(w["time"]) && !isTime(w["time"]) {
			return nil, domain.UserError("เวลารายงานต้องเป็น HH:MM")
		}
		day := int(jsNumber(w["day"]))
		if day == 0 {
			day = 5
		}
		out["weekly"] = domain.WeeklyPrefs{Enabled: jsTruthy(w["enabled"]), Day: day, Time: timeOr(w["time"], "16:00"), To: emailList(w["to"]), Cc: emailList(w["cc"])}
	}
	if raw, ok := in["nudge"]; ok && jsTruthy(raw) {
		var n map[string]json.RawMessage
		_ = json.Unmarshal(raw, &n)
		if hasTime(n["time"]) && !isTime(n["time"]) {
			return nil, domain.UserError("เวลาทวงอัปเดตต้องเป็น HH:MM")
		}
		day := int(jsNumber(n["day"]))
		if day == 0 {
			day = 4
		}
		out["nudge"] = domain.NudgePrefs{Enabled: jsTruthy(n["enabled"]), Day: day, Time: timeOr(n["time"], "09:00")}
	}
	if raw, ok := in["staleDays"]; ok {
		v := jsNumber(raw)
		if v == 0 {
			v = 5
		}
		out["staleDays"] = int(clampF(v, 1, 60))
	}
	if raw, ok := in["meetingPrepMinutes"]; ok {
		out["meetingPrepMinutes"] = int(clampF(jsNumber(raw), 0, 120))
	}
	if raw, ok := in["manDayHours"]; ok {
		v := jsNumber(raw)
		if v == 0 {
			v = 8
		}
		out["manDayHours"] = clampF(v, 1, 24)
	}
	if raw, ok := in["milestoneAlertDays"]; ok {
		days := []int{}
		for _, x := range listOf(raw, numSplit) {
			if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil && f > 0 && f < 120 && f == math.Trunc(f) {
				days = append(days, int(f))
			}
		}
		out["milestoneAlertDays"] = days
	}
	if raw, ok := in["workdaysOnly"]; ok {
		out["workdaysOnly"] = jsTruthy(raw)
	}
	return out, nil
}

// SavePrefs applies a prefs patch and returns the new prefs.
func (a *App) SavePrefs(body []byte) (domain.Prefs, error) {
	patch, err := cleanPrefs(body)
	if err != nil {
		return domain.Prefs{}, err
	}
	var outErr error
	a.Store.Update(func(s *domain.AppState) {
		cur := map[string]any{}
		b, _ := json.Marshal(s.Prefs)
		_ = json.Unmarshal(b, &cur)
		for k, v := range patch {
			cur[k] = v
		}
		b, _ = json.Marshal(cur)
		var np domain.Prefs
		if err := json.Unmarshal(b, &np); err != nil {
			outErr = domain.UserError("ค่าที่ตั้งไม่ถูกรูปแบบ: " + err.Error())
			return
		}
		s.Prefs = np
	})
	if outErr != nil {
		return domain.Prefs{}, outErr
	}
	return a.Store.Prefs(), nil
}

// ---------------------------------------------------------------- data

// Download is a file for the browser.
type Download struct {
	Name        string
	ContentType string
	Body        []byte
}

var nonAlnum = regexp.MustCompile(`(?i)[^a-z0-9]+`)

func csvCell(v any) string {
	var s string
	switch x := v.(type) {
	case nil:
		s = ""
	case string:
		s = x
	case time.Time:
		s = x.UTC().Format("2006-01-02T15:04:05.000Z")
	case *time.Time:
		if x != nil {
			s = x.UTC().Format("2006-01-02T15:04:05.000Z")
		}
	case json.RawMessage:
		s = string(x)
	case []byte:
		s = string(x)
	case int, int32, int64, float32, float64, bool:
		s = fmt.Sprint(x)
	default:
		b, _ := json.Marshal(x)
		s = string(b)
	}
	if strings.ContainsAny(s, "\",\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// CSV writes a table with a BOM so Excel reads Thai correctly.
func CSV(t port.Table) string {
	var b strings.Builder
	b.WriteString("\ufeff")
	if len(t.Rows) == 0 {
		return b.String()
	}
	b.WriteString(strings.Join(t.Columns, ","))
	for _, r := range t.Rows {
		b.WriteString("\n")
		cells := make([]string, len(t.Columns))
		for i, c := range t.Columns {
			cells[i] = csvCell(r[c])
		}
		b.WriteString(strings.Join(cells, ","))
	}
	return b.String()
}

// Export gives one workspace's data as JSON, or one table as CSV.
func (a *App) Export(ctx context.Context, ws, table string) (Download, error) {
	if ws == "" {
		ws = a.ws()
	}
	data, err := a.History.Export(ctx, ws)
	if err != nil {
		return Download{}, err
	}
	now := a.Now().UTC()
	name := "pm-assistant-" + nonAlnum.ReplaceAllString(ws, "-") + "-" + now.Format("2006-01-02")
	tables := map[string]port.Table{"metrics": data.Metrics, "activity": data.Activity, "records": data.Records}
	if t, ok := tables[table]; ok {
		return Download{Name: name + "-" + table + ".csv", ContentType: "text/csv; charset=utf-8", Body: []byte(CSV(t))}, nil
	}
	doc := struct {
		Workspace  string     `json:"workspace"`
		ExportedAt string     `json:"exportedAt"`
		Metrics    port.Table `json:"metrics"`
		Activity   port.Table `json:"activity"`
		Records    port.Table `json:"records"`
	}{ws, now.Format("2006-01-02T15:04:05.000Z"), data.Metrics, data.Activity, data.Records}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return Download{}, err
	}
	return Download{Name: name + ".json", ContentType: "application/json", Body: bytes.TrimRight(buf.Bytes(), "\n")}, nil
}

// DeleteData deletes one workspace's history, or everything (scope "all", confirmed by typing "ลบทั้งหมด").
func (a *App) DeleteData(ctx context.Context, scope, confirm, ws string) error {
	if scope == "all" {
		if confirm != "ลบทั้งหมด" {
			return domain.UserError(`พิมพ์ "ลบทั้งหมด" เพื่อยืนยัน`)
		}
		if err := a.History.DeleteWorkspace(ctx, nil); err != nil {
			return err
		}
		a.Store.Wipe()
		if a.Builtin != nil {
			if err := a.Builtin.Migrate(ctx); err != nil {
				return err
			}
		}
		a.Tasks.Reset()
		return nil
	}
	if ws == "" {
		return domain.UserError("ไม่ได้ระบุ workspace")
	}
	return a.History.DeleteWorkspace(ctx, &ws)
}

// ---------------------------------------------------------------- Plane webhook

// PlaneWebhook alerts the owner when a work item moves to a Blocked state. The signature is checked by the caller.
func (a *App) PlaneWebhook(ctx context.Context, body []byte, push Push) {
	var ev struct {
		Event    string `json:"event"`
		Action   string `json:"action"`
		Activity struct {
			Field    string `json:"field"`
			NewValue any    `json:"new_value"`
		} `json:"activity"`
		Data struct {
			Project    string `json:"project"`
			ProjectID  string `json:"project_id"`
			SequenceID any    `json:"sequence_id"`
			Name       string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		slog.Error("plane webhook handling failed", "err", err)
		return
	}
	nv := ""
	if ev.Activity.NewValue != nil {
		nv = fmt.Sprint(ev.Activity.NewValue)
	}
	isIssue := ev.Event == "issue" || ev.Event == "work_item"
	pid := ev.Data.Project
	if pid == "" {
		pid = ev.Data.ProjectID
	}
	if pid != "" && (isIssue || ev.Event == "cycle" || ev.Event == "module") {
		a.ForgetDashboard(pid) // the dashboard shows the change without waiting for the 15-minute round
	}
	if !isIssue || ev.Action != "updated" || ev.Activity.Field != "state" || !blockRe.MatchString(nv) || a.Cfg.OwnerUserID == "" {
		return
	}
	ps, err := a.tasks().Projects(ctx)
	if err != nil {
		slog.Error("plane webhook handling failed", "err", err)
		return
	}
	for _, p := range ps {
		if p.ID != pid {
			continue
		}
		seq := ""
		switch x := ev.Data.SequenceID.(type) {
		case float64:
			seq = num(x)
		case nil:
			seq = "undefined"
		default:
			seq = fmt.Sprint(x)
		}
		key := p.Identifier + "-" + seq
		if err := push(ctx, []port.Message{a.View.Text(fmt.Sprintf("แจ้งเตือนจาก Plane\n%s %s\nถูกเปลี่ยนเป็น %s", key, ev.Data.Name, nv))}); err != nil {
			slog.Error("plane webhook push failed", "err", err)
		}
	}
}
