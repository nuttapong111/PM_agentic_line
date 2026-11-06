// Package google calls Google OAuth, Calendar (events, free/busy, Meet links) and Gmail.
// It holds no credentials: callers pass the access token.
package google

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/adapter/crypto"
	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

var scopes = []string{
	"openid",
	"email",
	"https://www.googleapis.com/auth/calendar.readonly",
	"https://www.googleapis.com/auth/calendar.events",
	"https://www.googleapis.com/auth/gmail.send",
}

type Client struct {
	http *http.Client
	// Endpoint bases, replaceable in tests.
	OAuthURL, TokenURL, RevokeURL, CalendarURL, GmailURL string
}

func New(hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		http:        hc,
		OAuthURL:    "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:    "https://oauth2.googleapis.com/token",
		RevokeURL:   "https://oauth2.googleapis.com/revoke",
		CalendarURL: "https://www.googleapis.com/calendar/v3",
		GmailURL:    "https://gmail.googleapis.com/gmail/v1",
	}
}

func (c *Client) AuthURL(clientID, redirectURI, state string) string {
	q := url.Values{
		"client_id": {clientID}, "redirect_uri": {redirectURI}, "response_type": {"code"}, "scope": {strings.Join(scopes, " ")},
		"access_type": {"offline"}, "prompt": {"consent select_account"}, "include_granted_scopes": {"true"}, "state": {state},
	}
	return c.OAuthURL + "?" + q.Encode()
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	IDToken          string `json:"id_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (c *Client) token(ctx context.Context, form url.Values) (tokenResponse, int, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.http.Do(req)
	if err != nil {
		return tokenResponse{}, 0, err
	}
	defer res.Body.Close()
	var j tokenResponse
	_ = json.NewDecoder(res.Body).Decode(&j)
	return j, res.StatusCode, nil
}

func (c *Client) ExchangeCode(ctx context.Context, clientID, secret, redirectURI, code string) (port.GoogleTokens, error) {
	j, status, err := c.token(ctx, url.Values{"code": {code}, "client_id": {clientID}, "client_secret": {secret}, "redirect_uri": {redirectURI}, "grant_type": {"authorization_code"}})
	if err != nil {
		return port.GoogleTokens{}, err
	}
	if status >= 300 {
		why := j.ErrorDescription
		if why == "" {
			why = j.Error
		}
		if why == "" {
			why = fmt.Sprint(status)
		}
		return port.GoogleTokens{}, fmt.Errorf("Google ปฏิเสธ: %s", why)
	}
	if j.RefreshToken == "" {
		return port.GoogleTokens{}, fmt.Errorf("Google ไม่ส่ง refresh token กลับมา ลองยกเลิกสิทธิ์แอปใน myaccount.google.com แล้วเชื่อมใหม่")
	}
	return port.GoogleTokens{Refresh: j.RefreshToken, Access: j.AccessToken, ExpiresIn: j.ExpiresIn, Email: emailFromIDToken(j.IDToken)}, nil
}

func emailFromIDToken(t string) string {
	parts := strings.Split(t, ".")
	if len(parts) < 2 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var c struct {
		Email string `json:"email"`
	}
	_ = json.Unmarshal(b, &c)
	return c.Email
}

func (c *Client) RefreshAccess(ctx context.Context, clientID, secret, refresh string) (string, int, error) {
	j, status, err := c.token(ctx, url.Values{"client_id": {clientID}, "client_secret": {secret}, "refresh_token": {refresh}, "grant_type": {"refresh_token"}})
	if err != nil {
		return "", 0, err
	}
	if status >= 300 {
		why := j.Error
		if why == "" {
			why = fmt.Sprint(status)
		}
		return "", 0, fmt.Errorf("Google sign-in expired (%s): เชื่อม Google ใหม่ที่หน้าตั้งค่า", why)
	}
	return j.AccessToken, j.ExpiresIn, nil
}

func (c *Client) Revoke(ctx context.Context, refresh string) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.RevokeURL+"?token="+url.QueryEscape(refresh), nil)
	if res, err := c.http.Do(req); err == nil {
		res.Body.Close()
	}
}

func (c *Client) api(ctx context.Context, token, method, u string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	text, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return &domain.StatusError{Status: res.StatusCode, Msg: fmt.Sprintf("Google API %d: %s", res.StatusCode, domain.Truncate(string(text), 200))}
	}
	if out == nil || len(text) == 0 {
		return nil
	}
	return json.Unmarshal(text, out)
}

// ---------------------------------------------------------------- Calendar

type rawEvent struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
	Start   struct {
		DateTime string `json:"dateTime"`
		Date     string `json:"date"`
	} `json:"start"`
	End struct {
		DateTime string `json:"dateTime"`
		Date     string `json:"date"`
	} `json:"end"`
	HangoutLink    string `json:"hangoutLink"`
	ConferenceData struct {
		EntryPoints []struct {
			EntryPointType string `json:"entryPointType"`
			URI            string `json:"uri"`
		} `json:"entryPoints"`
	} `json:"conferenceData"`
	HTMLLink  string `json:"htmlLink"`
	Attendees []struct {
		Email    string `json:"email"`
		Resource bool   `json:"resource"`
	} `json:"attendees"`
	Description string `json:"description"`
}

func (e rawEvent) event() domain.CalEvent {
	ev := domain.CalEvent{
		ID: e.ID, Summary: e.Summary, Start: e.Start.DateTime, End: e.End.DateTime, AllDay: e.Start.DateTime == "",
		MeetURL: e.HangoutLink, HTMLLink: e.HTMLLink, Attendees: []string{}, Description: e.Description,
	}
	if ev.Summary == "" {
		ev.Summary = "(ไม่มีชื่อ)"
	}
	if ev.Start == "" {
		ev.Start = e.Start.Date
	}
	if ev.End == "" {
		ev.End = e.End.Date
	}
	if ev.MeetURL == "" {
		for _, p := range e.ConferenceData.EntryPoints {
			if p.EntryPointType == "video" {
				ev.MeetURL = p.URI
				break
			}
		}
	}
	for _, a := range e.Attendees {
		if !a.Resource {
			ev.Attendees = append(ev.Attendees, a.Email)
		}
	}
	return ev
}

func isoZ(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func (c *Client) ListEvents(ctx context.Context, token, calendarID string, from, to time.Time, q string) ([]domain.CalEvent, error) {
	if calendarID == "" {
		calendarID = "primary"
	}
	qs := url.Values{"timeMin": {isoZ(from)}, "timeMax": {isoZ(to)}, "singleEvents": {"true"}, "orderBy": {"startTime"}, "maxResults": {"50"}}
	if q != "" {
		qs.Set("q", q)
	}
	var j struct {
		Items []rawEvent `json:"items"`
	}
	if err := c.api(ctx, token, http.MethodGet, c.CalendarURL+"/calendars/"+url.PathEscape(calendarID)+"/events?"+qs.Encode(), nil, &j); err != nil {
		return nil, err
	}
	out := []domain.CalEvent{}
	for _, e := range j.Items {
		if e.Status != "cancelled" {
			out = append(out, e.event())
		}
	}
	return out, nil
}

func (c *Client) FreeBusy(ctx context.Context, token string, emails []string, from, to time.Time, tz string) (map[string][]domain.Interval, []string, error) {
	items := make([]map[string]string, len(emails))
	for i, e := range emails {
		items[i] = map[string]string{"id": e}
	}
	var j struct {
		Calendars map[string]struct {
			Busy []struct {
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"busy"`
			Errors []json.RawMessage `json:"errors"`
		} `json:"calendars"`
	}
	body := map[string]any{"timeMin": isoZ(from), "timeMax": isoZ(to), "timeZone": tz, "items": items}
	if err := c.api(ctx, token, http.MethodPost, c.CalendarURL+"/freeBusy", body, &j); err != nil {
		return nil, nil, err
	}
	busy := map[string][]domain.Interval{}
	unknown := []string{}
	for _, e := range emails {
		cal, ok := j.Calendars[e]
		if !ok || len(cal.Errors) > 0 {
			unknown = append(unknown, e)
			continue
		}
		iv := []domain.Interval{}
		for _, b := range cal.Busy {
			s, _ := domain.ParseInstant(b.Start)
			en, _ := domain.ParseInstant(b.End)
			iv = append(iv, domain.Interval{Start: s, End: en})
		}
		busy[e] = iv
	}
	return busy, unknown, nil
}

func (c *Client) InsertMeeting(ctx context.Context, token string, m port.MeetingRequest) (domain.CalEvent, error) {
	att := make([]map[string]string, len(m.Attendees))
	for i, a := range m.Attendees {
		att[i] = map[string]string{"email": a}
	}
	body := map[string]any{
		"summary":   m.Summary,
		"start":     map[string]string{"dateTime": m.StartISO, "timeZone": m.TimeZone},
		"end":       map[string]string{"dateTime": m.EndISO, "timeZone": m.TimeZone},
		"attendees": att,
		"conferenceData": map[string]any{"createRequest": map[string]any{
			"requestId": crypto.RandomID(8), "conferenceSolutionKey": map[string]string{"type": "hangoutsMeet"},
		}},
	}
	if m.Description != "" {
		body["description"] = m.Description
	}
	var e rawEvent
	err := c.api(ctx, token, http.MethodPost, c.CalendarURL+"/calendars/primary/events?conferenceDataVersion=1&sendUpdates=all", body, &e)
	return e.event(), err
}

// ---------------------------------------------------------------- Gmail

// MIME builds the raw message: base64 HTML body wrapped at 76 characters, UTF-8 subject.
func MIME(e port.Email) string {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	body := b64(e.HTML)
	var wrapped strings.Builder
	for len(body) > 76 {
		wrapped.WriteString(body[:76] + "\r\n")
		body = body[76:]
	}
	wrapped.WriteString(body)
	lines := []string{"To: " + strings.Join(e.To, ", ")}
	if len(e.Cc) > 0 {
		lines = append(lines, "Cc: "+strings.Join(e.Cc, ", "))
	}
	lines = append(lines,
		"Subject: =?UTF-8?B?"+b64(e.Subject)+"?=",
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
		"Content-Transfer-Encoding: base64",
		"",
		wrapped.String(),
	)
	return strings.Join(lines, "\r\n")
}

func (c *Client) SendEmail(ctx context.Context, token string, e port.Email) (string, error) {
	raw := base64.RawURLEncoding.EncodeToString([]byte(MIME(e)))
	var j struct {
		ID string `json:"id"`
	}
	err := c.api(ctx, token, http.MethodPost, c.GmailURL+"/users/me/messages/send", map[string]string{"raw": raw}, &j)
	return j.ID, err
}
