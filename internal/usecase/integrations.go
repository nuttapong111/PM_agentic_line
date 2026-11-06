package usecase

import (
	"context"
	"sync"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/config"
	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

// Google connects the owner's company Google account: OAuth, Calendar, Meet, Gmail.
// Client ID/secret come from the Settings page; tokens are stored encrypted.
type Google struct {
	api     port.GoogleAPI
	store   *Store
	secrets port.Secrets
	cfg     config.Config
	now     func() time.Time
	loc     *time.Location

	mu     sync.Mutex
	access string
	exp    time.Time
}

func (g *Google) Connected() bool {
	c := g.store.Connections().Google
	return c != nil && c.RefreshEnc != "" && c.ClientID != ""
}

func (g *Google) RedirectURI() string { return g.cfg.PublicURL + "/oauth/google/callback" }

// AuthURL is step 1: the URL the owner opens to sign in.
func (g *Google) AuthURL() (string, error) {
	c := g.store.Connections().Google
	if c == nil || c.ClientID == "" {
		return "", domain.UserError("ใส่ Client ID และ Client secret ก่อน")
	}
	state := randomHex(16)
	g.store.Update(func(s *domain.AppState) { s.OAuth = &domain.OAuthState{State: state, At: g.now().UnixMilli()} })
	return g.api.AuthURL(c.ClientID, g.RedirectURI(), state), nil
}

// HandleCallback is step 2: exchange the code for a refresh token.
func (g *Google) HandleCallback(ctx context.Context, code, state string) (string, error) {
	snap := g.store.Snapshot()
	if snap.OAuth == nil || snap.OAuth.State != state || g.now().UnixMilli()-snap.OAuth.At > 15*60_000 {
		return "", domain.UserError("ลิงก์เชื่อมหมดอายุ ลองกดเชื่อมใหม่จากหน้าตั้งค่า")
	}
	c := snap.Connections.Google
	if c == nil {
		return "", domain.UserError("ไม่พบ Client secret")
	}
	secret, ok := g.secrets.Decrypt(c.ClientSecretEnc)
	if !ok {
		return "", domain.UserError("ไม่พบ Client secret")
	}
	t, err := g.api.ExchangeCode(ctx, c.ClientID, secret, g.RedirectURI(), code)
	if err != nil {
		return "", err
	}
	g.setAccess(t.Access, t.ExpiresIn)
	enc := g.secrets.Encrypt(t.Refresh)
	g.store.Update(func(s *domain.AppState) {
		s.OAuth = nil
		if s.Connections.Google == nil {
			s.Connections.Google = &domain.GoogleConnection{}
		}
		s.Connections.Google.RefreshEnc = enc
		s.Connections.Google.Email = t.Email
		s.Connections.Google.ConnectedAt = g.now().UnixMilli()
	})
	return t.Email, nil
}

func (g *Google) setAccess(token string, expiresIn int) {
	g.mu.Lock()
	g.access, g.exp = token, g.now().Add(time.Duration(expiresIn-60)*time.Second)
	g.mu.Unlock()
}

func (g *Google) Disconnect(ctx context.Context) {
	if c := g.store.Connections().Google; c != nil {
		if refresh, ok := g.secrets.Decrypt(c.RefreshEnc); ok {
			go g.api.Revoke(context.WithoutCancel(ctx), refresh)
		}
	}
	g.mu.Lock()
	g.access = ""
	g.mu.Unlock()
	g.store.Update(func(s *domain.AppState) {
		if s.Connections.Google != nil {
			s.Connections.Google.RefreshEnc, s.Connections.Google.Email, s.Connections.Google.ConnectedAt = "", "", 0
		}
	})
}

func (g *Google) token(ctx context.Context) (string, error) {
	g.mu.Lock()
	if g.access != "" && g.exp.After(g.now()) {
		t := g.access
		g.mu.Unlock()
		return t, nil
	}
	g.mu.Unlock()
	c := g.store.Connections().Google
	if c == nil {
		return "", domain.ErrGoogleNotConnected
	}
	refresh, ok1 := g.secrets.Decrypt(c.RefreshEnc)
	secret, ok2 := g.secrets.Decrypt(c.ClientSecretEnc)
	if !ok1 || !ok2 {
		return "", domain.ErrGoogleNotConnected
	}
	access, exp, err := g.api.RefreshAccess(ctx, c.ClientID, secret, refresh)
	if err != nil {
		return "", err
	}
	g.setAccess(access, exp)
	return access, nil
}

func (g *Google) Email() string {
	if c := g.store.Connections().Google; c != nil {
		return c.Email
	}
	return ""
}

func (g *Google) ListEvents(ctx context.Context, from, to time.Time, calendarID, q string) ([]domain.CalEvent, error) {
	tok, err := g.token(ctx)
	if err != nil {
		return nil, err
	}
	return g.api.ListEvents(ctx, tok, calendarID, from, to, q)
}

// ListMail searches the owner's Gmail (headers and snippets only).
func (g *Google) ListMail(ctx context.Context, query string, max int) ([]domain.MailItem, error) {
	tok, err := g.token(ctx)
	if err != nil {
		return nil, err
	}
	return g.api.ListMail(ctx, tok, query, max)
}

// TodaysMeetings lists today's timed events.
func (g *Google) TodaysMeetings(ctx context.Context) ([]domain.CalEvent, error) {
	today := domain.DateIn(g.now(), g.loc)
	evs, err := g.ListEvents(ctx, domain.LocalToTime(today, "00:00", g.loc), domain.LocalToTime(today, "23:59", g.loc), "primary", "")
	if err != nil {
		return nil, err
	}
	out := []domain.CalEvent{}
	for _, e := range evs {
		if !e.AllDay {
			out = append(out, e)
		}
	}
	return out, nil
}

// FindSlots looks up free/busy and lists candidate slots, best first.
func (g *Google) FindSlots(ctx context.Context, emails []string, fromDate string, days, minutes int) ([]domain.Slot, []string, error) {
	tok, err := g.token(ctx)
	if err != nil {
		return nil, nil, err
	}
	start := domain.LocalToTime(fromDate, "00:00", g.loc)
	end := start.Add(time.Duration(days) * 24 * time.Hour)
	busy, unknown, err := g.api.FreeBusy(ctx, tok, emails, start, end, g.cfg.Timezone)
	if err != nil {
		return nil, nil, err
	}
	known := []string{}
	for _, e := range emails {
		if _, ok := busy[e]; ok {
			known = append(known, e)
		}
	}
	return domain.FindSlots(known, busy, fromDate, days, minutes, g.now(), g.loc), unknown, nil
}

func (g *Google) CreateMeeting(ctx context.Context, m port.MeetingRequest) (domain.CalEvent, error) {
	tok, err := g.token(ctx)
	if err != nil {
		return domain.CalEvent{}, err
	}
	m.TimeZone = g.cfg.Timezone
	return g.api.InsertMeeting(ctx, tok, m)
}

// LeaveDays reads a shared leave calendar for the people in [from, to].
func (g *Google) LeaveDays(ctx context.Context, calendarID, from, to string, people []domain.Person) (map[string][]string, error) {
	evs, err := g.ListEvents(ctx, domain.LocalToTime(from, "00:00", g.loc), domain.LocalToTime(to, "23:59", g.loc), calendarID, "")
	if err != nil {
		return nil, err
	}
	return domain.LeaveDays(evs, people, from, to), nil
}

func (g *Google) SendEmail(ctx context.Context, e port.Email) (string, error) {
	tok, err := g.token(ctx)
	if err != nil {
		return "", err
	}
	return g.api.SendEmail(ctx, tok, e)
}

// Notion reads and writes the owner's Notion with the integration token from Settings.
type Notion struct {
	api     port.NotionAPI
	store   *Store
	secrets port.Secrets
}

func (n *Notion) Connected() bool {
	c := n.store.Connections().Notion
	return c != nil && c.TokenEnc != ""
}

func (n *Notion) token() (string, error) {
	c := n.store.Connections().Notion
	if c == nil {
		return "", domain.ErrNotionNotConnected
	}
	t, ok := n.secrets.Decrypt(c.TokenEnc)
	if !ok {
		return "", domain.ErrNotionNotConnected
	}
	return t, nil
}

func (n *Notion) Whoami(ctx context.Context, token string) (string, error) {
	return n.api.Whoami(ctx, token)
}

func (n *Notion) Search(ctx context.Context, q string, limit int) ([]domain.NotionPage, error) {
	t, err := n.token()
	if err != nil {
		return nil, err
	}
	return n.api.Search(ctx, t, q, limit)
}

func (n *Notion) PageText(ctx context.Context, id string, max int) (domain.PageText, error) {
	t, err := n.token()
	if err != nil {
		return domain.PageText{}, err
	}
	return n.api.PageText(ctx, t, id, max)
}

func (n *Notion) CreatePage(ctx context.Context, parent, title, md string) (domain.NotionPage, error) {
	t, err := n.token()
	if err != nil {
		return domain.NotionPage{}, err
	}
	return n.api.CreatePage(ctx, t, parent, title, md)
}

func (n *Notion) Append(ctx context.Context, pageID, md string) error {
	t, err := n.token()
	if err != nil {
		return err
	}
	return n.api.Append(ctx, t, pageID, md)
}
