package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/adapter/crypto"
	"github.com/nuttapong111/pm_agentic_line/internal/adapter/filestore"
	"github.com/nuttapong111/pm_agentic_line/internal/adapter/line"
	"github.com/nuttapong111/pm_agentic_line/internal/config"
	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
	"github.com/nuttapong111/pm_agentic_line/internal/usecase"
)

type fakeLine struct {
	mu      sync.Mutex
	replies [][]port.Message
	done    chan struct{}
}

func (f *fakeLine) Reply(_ context.Context, _ string, m []port.Message) error {
	f.mu.Lock()
	f.replies = append(f.replies, m)
	f.mu.Unlock()
	f.done <- struct{}{}
	return nil
}
func (f *fakeLine) Push(context.Context, string, []port.Message) error { return nil }
func (f *fakeLine) ShowLoading(context.Context, string, int) error     { return nil }
func (f *fakeLine) Content(context.Context, string) ([]byte, error)    { return nil, nil }

func newServer(t *testing.T, owner string) (*Server, *fakeLine) {
	vars := map[string]string{"LINE_CHANNEL_SECRET": "sec", "LINE_CHANNEL_ACCESS_TOKEN": "x", "DASHBOARD_TOKEN": "tok", "OWNER_LINE_USER_ID": owner}
	cfg, err := config.LoadFrom(func(k string) string { return vars[k] })
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Bangkok")
	d := domain.PrefDefaults{DigestCron: cfg.DigestCron, StaleDays: 5, BugLabel: "bug"}
	store, err := usecase.NewStore(context.Background(), filestore.New(t.TempDir()), nil, d, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	secrets := crypto.New("k")
	tasks := usecase.NewTasks(cfg, store, secrets, usecase.NoHistory{}, nil, nil)
	app := usecase.New(usecase.Deps{Cfg: cfg, Loc: loc, Store: store, Tasks: tasks, Secrets: secrets, View: line.Presenter{}})
	fl := &fakeLine{done: make(chan struct{}, 4)}
	return &Server{App: app, Cfg: cfg, Line: fl, DashboardHTML: []byte("<html>dash</html>"), SettingsHTML: []byte("<html>set</html>")}, fl
}

func sign(body string) string {
	m := hmac.New(sha256.New, []byte("sec"))
	m.Write([]byte(body))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func TestWebhook(t *testing.T) {
	s, fl := newServer(t, "")
	h := s.Handler()
	body := `{"events":[{"type":"message","replyToken":"r1","source":{"type":"user","userId":"Uabc"},"message":{"type":"text","id":"1","text":"hi"}}]}`

	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(body))
	req.Header.Set("x-line-signature", "bad")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("bad signature: %d", w.Code)
	}

	req = httptest.NewRequest("POST", "/webhook", strings.NewReader(body))
	req.Header.Set("x-line-signature", sign(body))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("webhook: %d", w.Code)
	}
	select {
	case <-fl.done:
	case <-time.After(2 * time.Second):
		t.Fatal("no reply")
	}
	if got := toText(fl.replies[0]); !strings.Contains(got, "Uabc") {
		t.Fatalf("first contact should get the user ID back, got %s", got)
	}
}

func toText(ms []port.Message) string {
	var b strings.Builder
	for _, m := range ms {
		if s, ok := m["text"].(string); ok {
			b.WriteString(s)
		}
	}
	return b.String()
}

func TestAuthAndPages(t *testing.T) {
	s, _ := newServer(t, "U1")
	h := s.Handler()
	cases := []struct {
		path, token string
		want        int
		body        string
	}{
		{"/healthz", "", 200, `"ok":true`},
		{"/dashboard?t=tok", "", 200, "dash"},
		{"/dashboard?t=nope", "", 401, ""},
		{"/settings?t=tok", "", 200, "set"},
		{"/api/settings", "tok", 200, `"model"`},
		{"/api/settings", "", 401, "unauthorized"},
		{"/api/dashboard?t=tok", "", 200, `"projects":[]`},
		{"/api/records?t=tok", "", 200, `"enabled":false`},
		{"/api/usage?t=tok", "", 200, `"month"`},
		{"/preview/abc?t=tok", "", 404, ""},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", c.path, nil)
		if c.token != "" {
			req.Header.Set("x-token", c.token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != c.want || !strings.Contains(w.Body.String(), c.body) {
			t.Errorf("%s: %d %s", c.path, w.Code, w.Body.String())
		}
	}
	// prefs validation via HTTP
	req := httptest.NewRequest("POST", "/api/settings/prefs", strings.NewReader(`{"quietFrom":"9am"}`))
	req.Header.Set("x-token", "tok")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "quietFrom ต้องเป็นเวลา HH:MM") {
		t.Errorf("prefs: %d %s", w.Code, w.Body.String())
	}
	// Plane webhook is off without a secret
	req = httptest.NewRequest("POST", "/plane/webhook", strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("plane webhook: %d", w.Code)
	}
}

var _ http.Handler = (*http.ServeMux)(nil)
