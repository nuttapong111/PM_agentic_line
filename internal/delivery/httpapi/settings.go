package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/nuttapong111/pm_agentic_line/internal/usecase"
)

// readBody reads a JSON body; anything unreadable is treated as {}.
func readBody(r *http.Request) []byte {
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || len(b) == 0 || !json.Valid(b) {
		return []byte("{}")
	}
	return b
}

// fields decodes a body into named raw fields.
func fields(r *http.Request) map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	_ = json.Unmarshal(readBody(r), &m)
	return m
}

// str reads a field the way the page sends it: a string, else its text form, else "".
func str(m map[string]json.RawMessage, k string) string {
	raw, ok := m[k]
	if !ok {
		return ""
	}
	var v any
	if json.Unmarshal(raw, &v) != nil || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if f, ok := v.(float64); ok {
		return fmt.Sprint(f)
	}
	return fmt.Sprint(v)
}

// strs reads an array field as strings; anything else is an empty list.
func strs(m map[string]json.RawMessage, k string) []string {
	var arr []any
	out := []string{}
	if json.Unmarshal(m[k], &arr) != nil {
		return out
	}
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		} else {
			out = append(out, fmt.Sprint(v))
		}
	}
	return out
}

var ok = map[string]bool{"ok": true}

func (s *Server) settingsRoutes(m *http.ServeMux) {
	a := s.App
	h := func(pattern string, fn http.HandlerFunc) { m.HandleFunc(pattern, s.auth(fn)) }

	m.HandleFunc("GET /settings", func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			text(w, 401, `ลิงก์ไม่ถูกต้อง พิมพ์ "ตั้งค่า" ในแชตบอทเพื่อรับลิงก์ใหม่`)
			return
		}
		page(w, 200, s.SettingsHTML)
	})
	h("GET /api/settings", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.SettingsSnapshot(r.Context())) })

	// ---------------- task tool
	planeIn := func(r *http.Request) usecase.PlaneInput {
		b := fields(r)
		return usecase.PlaneInput{BaseURL: str(b, "baseUrl"), Workspace: str(b, "workspace"), Token: str(b, "token"), Projects: strs(b, "projects")}
	}
	h("POST /api/settings/plane/test", func(w http.ResponseWriter, r *http.Request) {
		out, err := a.PlaneTest(r.Context(), planeIn(r))
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, out)
	})
	h("POST /api/settings/plane/save", func(w http.ResponseWriter, r *http.Request) {
		if err := a.PlaneSave(r.Context(), planeIn(r)); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, ok)
	})
	h("POST /api/settings/projects", func(w http.ResponseWriter, r *http.Request) {
		a.SetFollowed(strs(fields(r), "projects"))
		writeJSON(w, 200, ok)
	})
	h("POST /api/settings/plane/check", func(w http.ResponseWriter, r *http.Request) {
		name, err := a.PlaneCheck(r.Context())
		if err != nil {
			writeErr(w, 502, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "account": name})
	})
	h("POST /api/settings/provider", func(w http.ResponseWriter, r *http.Request) {
		if err := a.SetProvider(r.Context(), str(fields(r), "provider")); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, ok)
	})
	h("POST /api/settings/builtin/project", func(w http.ResponseWriter, r *http.Request) {
		b := fields(r)
		p, err := a.CreateBuiltinProject(r.Context(), str(b, "name"), str(b, "identifier"))
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "project": p})
	})

	// ---------------- Claude
	h("POST /api/settings/claude", func(w http.ResponseWriter, r *http.Request) {
		model, err := a.SaveClaudeKey(r.Context(), str(fields(r), "key"))
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "model": model})
	})
	h("POST /api/settings/claude/remove", func(w http.ResponseWriter, r *http.Request) { a.RemoveClaude(); writeJSON(w, 200, ok) })

	// ---------------- Notion
	h("POST /api/settings/notion", func(w http.ResponseWriter, r *http.Request) {
		name, err := a.SaveNotion(r.Context(), str(fields(r), "token"))
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "name": name})
	})
	h("POST /api/settings/notion/remove", func(w http.ResponseWriter, r *http.Request) { a.RemoveNotion(); writeJSON(w, 200, ok) })

	// ---------------- Google
	h("POST /api/settings/google", func(w http.ResponseWriter, r *http.Request) {
		b := fields(r)
		u, err := a.SaveGoogleClient(str(b, "clientId"), str(b, "clientSecret"))
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "url": u})
	})
	h("GET /api/settings/google/auth", func(w http.ResponseWriter, r *http.Request) {
		u, err := a.Google.AuthURL()
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"url": u})
	})
	h("POST /api/settings/google/remove", func(w http.ResponseWriter, r *http.Request) {
		a.Google.Disconnect(r.Context())
		writeJSON(w, 200, ok)
	})
	m.HandleFunc("GET /oauth/google/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		send := func(msg string, good bool) {
			w.Header().Set("Content-Type", "text/html; charset=UTF-8")
			w.Header().Set("Referrer-Policy", "no-referrer")
			if good {
				w.WriteHeader(200)
			} else {
				w.WriteHeader(400)
			}
			_, _ = io.WriteString(w, a.OAuthPage(msg, good))
		}
		if e := q.Get("error"); e != "" {
			send(e, false)
			return
		}
		email, err := a.Google.HandleCallback(r.Context(), q.Get("code"), q.Get("state"))
		if err != nil {
			send(err.Error(), false)
			return
		}
		send("เชื่อมในชื่อ "+email, true)
	})

	// ---------------- preferences
	h("POST /api/settings/prefs", func(w http.ResponseWriter, r *http.Request) {
		p, err := a.SavePrefs(readBody(r))
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "prefs": p})
	})

	// ---------------- data
	h("GET /api/settings/export", func(w http.ResponseWriter, r *http.Request) {
		d, err := a.Export(r.Context(), r.URL.Query().Get("ws"), r.URL.Query().Get("table"))
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		w.Header().Set("Content-Type", d.ContentType)
		w.Header().Set("Content-Disposition", `attachment; filename="`+d.Name+`"`)
		w.WriteHeader(200)
		_, _ = w.Write(d.Body)
	})
	h("POST /api/settings/delete", func(w http.ResponseWriter, r *http.Request) {
		b := fields(r)
		if err := a.DeleteData(r.Context(), str(b, "scope"), str(b, "confirm"), str(b, "ws")); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, ok)
	})
}
