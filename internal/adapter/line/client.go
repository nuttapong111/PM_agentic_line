// Package line talks to the LINE Messaging API: replies, pushes, image content, the rich menu,
// webhook signatures, and the Flex cards the bot sends.
package line

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

type Client struct {
	token string
	http  *http.Client
	API   string
	Data  string
}

func New(accessToken string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{token: accessToken, http: hc, API: "https://api.line.me", Data: "https://api-data.line.me"}
}

// VerifySignature checks x-line-signature: base64 HMAC-SHA256 of the body with the channel secret.
func VerifySignature(secret string, body []byte, sig string) bool {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	want := base64.StdEncoding.EncodeToString(m.Sum(nil))
	return hmac.Equal([]byte(want), []byte(sig))
}

func (c *Client) call(ctx context.Context, method, u string, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return &domain.StatusError{Status: res.StatusCode, Msg: fmt.Sprintf("LINE %s %s → %d %s", method, u, res.StatusCode, domain.Truncate(string(data), 300))}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, c.API+path, "application/json", bytes.NewReader(b), out)
}

func (c *Client) Reply(ctx context.Context, token string, msgs []port.Message) error {
	return c.post(ctx, "/v2/bot/message/reply", map[string]any{"replyToken": token, "messages": msgs}, nil)
}

func (c *Client) Push(ctx context.Context, to string, msgs []port.Message) error {
	return c.post(ctx, "/v2/bot/message/push", map[string]any{"to": to, "messages": msgs}, nil)
}

func (c *Client) ShowLoading(ctx context.Context, chatID string, seconds int) error {
	return c.post(ctx, "/v2/bot/chat/loading/start", map[string]any{"chatId": chatID, "loadingSeconds": seconds}, nil)
}

func (c *Client) Content(ctx context.Context, messageID string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Data+"/v2/bot/message/"+messageID+"/content", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("LINE content %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 20<<20))
}

// ---------------------------------------------------------------- rich menu

const menuName = "pm-assistant-v1"

type richMenu struct {
	RichMenuID string `json:"richMenuId"`
	Name       string `json:"name"`
	Areas      []struct {
		Action struct {
			URI string `json:"uri"`
		} `json:"action"`
	} `json:"areas"`
}

// EnsureRichMenu installs the 6-button menu as the default (idempotent). The Dashboard button
// carries the URL, so a new PUBLIC_URL or token means a new menu.
func (c *Client) EnsureRichMenu(ctx context.Context, dashboardURL string, image []byte, force bool) (string, error) {
	var list struct {
		Richmenus []richMenu `json:"richmenus"`
	}
	if err := c.call(ctx, http.MethodGet, c.API+"/v2/bot/richmenu/list", "", nil, &list); err != nil {
		return "", err
	}
	if !force {
		for _, m := range list.Richmenus {
			if m.Name != menuName {
				continue
			}
			for _, a := range m.Areas {
				if a.Action.URI == dashboardURL {
					return m.RichMenuID, c.call(ctx, http.MethodPost, c.API+"/v2/bot/user/all/richmenu/"+m.RichMenuID, "", nil, nil)
				}
			}
		}
	}
	const w, h = 2500.0 / 3, 1686.0 / 2
	pb := func(data, text string) map[string]any {
		return map[string]any{"type": "postback", "data": data, "displayText": text}
	}
	actions := []map[string]any{
		{"type": "uri", "uri": dashboardURL, "label": "Dashboard"},
		pb("cmd:status", "สถานะโปรเจกต์"),
		pb("cmd:mine", "งานของฉัน"),
		pb("cmd:create", "สร้างงาน"),
		pb("cmd:digest", "สรุปวันนี้"),
		pb("cmd:switch", "เปลี่ยนโปรเจกต์"),
	}
	areas := make([]map[string]any, len(actions))
	round := func(f float64) int { return int(f + 0.5) }
	for i, a := range actions {
		areas[i] = map[string]any{
			"bounds": map[string]int{"x": round(float64(i%3) * w), "y": round(float64(i/3) * h), "width": round(w), "height": round(h)},
			"action": a,
		}
	}
	var created struct {
		RichMenuID string `json:"richMenuId"`
	}
	if err := c.post(ctx, "/v2/bot/richmenu", map[string]any{
		"size": map[string]int{"width": 2500, "height": 1686}, "selected": true, "name": menuName, "chatBarText": "เมนู PM", "areas": areas,
	}, &created); err != nil {
		return "", err
	}
	if err := c.call(ctx, http.MethodPost, c.Data+"/v2/bot/richmenu/"+created.RichMenuID+"/content", "image/png", bytes.NewReader(image), nil); err != nil {
		return "", err
	}
	if err := c.call(ctx, http.MethodPost, c.API+"/v2/bot/user/all/richmenu/"+created.RichMenuID, "", nil, nil); err != nil {
		return "", err
	}
	// Remove our older menus so the list doesn't grow on each URL change.
	for _, m := range list.Richmenus {
		if m.Name == menuName {
			_ = c.call(ctx, http.MethodDelete, c.API+"/v2/bot/richmenu/"+m.RichMenuID, "", nil, nil)
		}
	}
	return created.RichMenuID, nil
}
