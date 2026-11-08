// Package httpapi is the HTTP delivery layer: LINE webhook, Plane webhook, dashboard and
// settings pages and their JSON APIs, Google OAuth callback, draft previews.
package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/adapter/line"
	"github.com/nuttapong111/pm_agentic_line/internal/config"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
	"github.com/nuttapong111/pm_agentic_line/internal/usecase"
)

const maxImage = 4.5 * 1024 * 1024 // Claude accepts up to ~5 MB

// Server wires HTTP requests to the use cases.
type Server struct {
	App           *usecase.App
	Cfg           config.Config
	Line          port.Messenger
	DashboardHTML []byte
	SettingsHTML  []byte

	mu sync.Mutex // one LINE event at a time, so the conversation state never interleaves
}

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { text(w, 200, "LINE PM Assistant is running") })
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]bool{"ok": true}) })

	m.HandleFunc("POST /webhook", s.lineWebhook)
	m.HandleFunc("POST /plane/webhook", s.planeWebhook)

	// dashboard
	m.HandleFunc("GET /dashboard", func(w http.ResponseWriter, r *http.Request) {
		if !s.queryToken(r) {
			text(w, 401, "ลิงก์ไม่ถูกต้อง ขอลิงก์ใหม่จากบอทใน LINE")
			return
		}
		page(w, 200, s.DashboardHTML)
	})
	m.HandleFunc("GET /api/dashboard", s.queryAuth(func(w http.ResponseWriter, r *http.Request) {
		out, err := s.App.Dashboard(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("fresh") == "1")
		if err != nil {
			slog.Error("dashboard api failed", "err", err)
			writeErr(w, 502, err)
			return
		}
		writeJSON(w, 200, out)
	}))
	m.HandleFunc("GET /api/history", s.queryAuth(func(w http.ResponseWriter, r *http.Request) {
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		out, err := s.App.HistoryData(r.Context(), r.URL.Query().Get("project"), days)
		if err != nil {
			slog.Error("history api failed", "err", err)
			writeJSON(w, 502, map[string]string{"error": "history unavailable"})
			return
		}
		writeJSON(w, 200, out)
	}))
	m.HandleFunc("GET /api/usage", s.queryAuth(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.App.Usage()) }))
	m.HandleFunc("GET /api/records", s.auth(func(w http.ResponseWriter, r *http.Request) {
		out, err := s.App.DashboardRecords(r.Context(), r.URL.Query().Get("project"))
		if err != nil {
			slog.Error("records api failed", "err", err)
			writeErr(w, 502, err)
			return
		}
		writeJSON(w, 200, out)
	}))
	m.HandleFunc("GET /preview/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			text(w, 401, "ลิงก์ไม่ถูกต้อง")
			return
		}
		html, ok := s.App.PreviewPage(r.PathValue("id"))
		if !ok {
			text(w, 404, "ร่างนี้ส่งไปแล้วหรือหมดอายุแล้ว")
			return
		}
		page(w, 200, []byte(html))
	})

	s.settingsRoutes(m)
	return m
}

// ---------------------------------------------------------------- auth

func safeEqual(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func (s *Server) queryToken(r *http.Request) bool {
	return safeEqual(r.URL.Query().Get("t"), s.Cfg.DashboardToken)
}

// authed takes the dashboard token from ?t= (pages) or the x-token header (API calls from the pages).
func (s *Server) authed(r *http.Request) bool {
	t := r.URL.Query().Get("t")
	if !r.URL.Query().Has("t") {
		t = r.Header.Get("x-token")
	}
	return safeEqual(t, s.Cfg.DashboardToken)
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		h(w, r)
	}
}

func (s *Server) queryAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.queryToken(r) {
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		h(w, r)
	}
}

// ---------------------------------------------------------------- responses

func text(w http.ResponseWriter, status int, t string) {
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, t)
}

func page(w http.ResponseWriter, status int, html []byte) {
	w.Header().Set("Content-Type", "text/html; charset=UTF-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_, _ = w.Write(html)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		slog.Error("json encode failed", "err", err)
		status, buf = 500, *bytes.NewBufferString(`{"error":"internal error"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// ---------------------------------------------------------------- LINE webhook

type lineEvent struct {
	Type       string `json:"type"`
	ReplyToken string `json:"replyToken"`
	Source     struct {
		Type   string `json:"type"`
		UserID string `json:"userId"`
	} `json:"source"`
	Message struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Text string `json:"text"`
	} `json:"message"`
	Postback struct {
		Data string `json:"data"`
	} `json:"postback"`
}

func (s *Server) lineWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		text(w, 400, "bad request")
		return
	}
	if !line.VerifySignature(s.Cfg.LineChannelSecret, body, r.Header.Get("x-line-signature")) {
		text(w, 401, "bad signature")
		return
	}
	var payload struct {
		Events []lineEvent `json:"events"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		text(w, 400, "bad request")
		return
	}
	// Answer LINE right away; do the work in the background.
	for _, ev := range payload.Events {
		go func(ev lineEvent) {
			defer func() {
				if p := recover(); p != nil {
					slog.Error("event panicked", "panic", p)
				}
			}()
			s.handleEvent(context.Background(), ev)
		}(ev)
	}
	text(w, 200, "ok")
}

func (s *Server) serial(fn func() []port.Message) []port.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn()
}

func (s *Server) handleEvent(ctx context.Context, ev lineEvent) {
	if ev.Source.Type != "user" || ev.Source.UserID == "" {
		return // groups come later
	}
	userID := ev.Source.UserID
	view := s.App.View
	reply := func(msgs []port.Message) {
		if ev.ReplyToken != "" {
			if err := s.Line.Reply(ctx, ev.ReplyToken, msgs); err != nil {
				slog.Error("reply failed", "err", err)
			}
		}
	}
	if s.Cfg.OwnerUserID == "" {
		reply([]port.Message{view.Text("ตั้งค่า OWNER_LINE_USER_ID เป็น\n" + userID + "\nแล้ว redeploy เพื่อเริ่มใช้งาน")})
		return
	}
	if userID != s.Cfg.OwnerUserID {
		reply([]port.Message{view.Text("บอทนี้ใช้งานส่วนตัวครับ")})
		return
	}

	started := time.Now()
	var msgs []port.Message
	loading := func(sec int) { _ = s.Line.ShowLoading(ctx, userID, sec) }
	switch {
	case ev.Type == "follow":
		msgs = s.App.Welcome(ctx)
	case ev.Type == "message" && ev.Message.Type == "text":
		loading(30)
		msgs = s.serial(func() []port.Message { return s.App.HandleText(ctx, ev.Message.Text) })
	case ev.Type == "postback":
		loading(20)
		msgs = s.serial(func() []port.Message { return s.App.HandleMenu(ctx, ev.Postback.Data) })
	case ev.Type == "message" && ev.Message.Type == "image":
		loading(45)
		msgs = s.serial(func() []port.Message {
			data, mediaType := s.readImage(ctx, ev.Message.ID)
			if data == "" {
				return []port.Message{view.Text("เปิดรูปนี้ไม่ได้ครับ ลองส่งเป็นรูปภาพ (JPEG/PNG) อีกครั้ง")}
			}
			return s.App.HandleImage(ctx, data, mediaType, "")
		})
	case ev.Type == "message":
		msgs = []port.Message{view.Text("ตอนนี้รับข้อความตัวอักษรและรูปภาพครับ")}
	}
	if len(msgs) == 0 {
		return
	}
	// Reply tokens expire after about a minute; fall back to push (counts toward the monthly quota).
	if ev.ReplyToken != "" && time.Since(started) < 50*time.Second {
		err := s.Line.Reply(ctx, ev.ReplyToken, msgs)
		if err == nil {
			return
		}
		slog.Warn("reply failed, falling back to push", "err", err)
	}
	if err := s.Line.Push(ctx, userID, msgs); err != nil {
		slog.Error("push failed", "err", err)
	}
}

// readImage downloads a photo the owner sent, as base64 + media type. "" when unusable.
func (s *Server) readImage(ctx context.Context, id string) (string, string) {
	b, err := s.Line.Content(ctx, id)
	if err != nil {
		slog.Error("image download failed", "err", err)
		return "", ""
	}
	if len(b) > maxImage {
		return "", ""
	}
	mt := "image/jpeg"
	if len(b) > 1 && b[0] == 0x89 && b[1] == 0x50 {
		mt = "image/png"
	}
	return base64.StdEncoding.EncodeToString(b), mt
}

// Push sends messages to the owner (the scheduler and the Plane webhook use it).
func (s *Server) Push(ctx context.Context, msgs []port.Message) error {
	if s.Cfg.OwnerUserID == "" || len(msgs) == 0 {
		return nil
	}
	if len(msgs) > 5 {
		msgs = msgs[:5]
	}
	return s.Line.Push(ctx, s.Cfg.OwnerUserID, msgs)
}

// ---------------------------------------------------------------- Plane webhook
// Plane → Workspace settings → Webhooks → URL https://<host>/plane/webhook, copy the secret into PLANE_WEBHOOK_SECRET.

func (s *Server) planeWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		text(w, 400, "bad request")
		return
	}
	if s.Cfg.PlaneWebhookSecret == "" {
		text(w, 404, "disabled")
		return
	}
	mac := hmac.New(sha256.New, []byte(s.Cfg.PlaneWebhookSecret))
	mac.Write(body)
	if !safeEqual(r.Header.Get("x-plane-signature"), hex.EncodeToString(mac.Sum(nil))) {
		text(w, 401, "bad signature")
		return
	}
	s.App.PlaneWebhook(r.Context(), body, s.Push)
	text(w, 200, "ok")
}
