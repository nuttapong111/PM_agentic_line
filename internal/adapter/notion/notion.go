// Package notion searches pages, reads a page as text and creates or appends pages.
// It uses an internal integration token; share pages with the integration in Notion.
package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
)

const version = "2022-06-28"

type Client struct {
	http *http.Client
	Base string
}

func New(hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{http: hc, Base: "https://api.notion.com/v1"}
}

func (c *Client) api(ctx context.Context, token, method, path string, body any, out any) error {
	if token == "" {
		return domain.ErrNotionNotConnected
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Notion-Version", version)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	text, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		msg := domain.Truncate(string(text), 200)
		var j struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(text, &j) == nil && j.Message != "" {
			msg = j.Message
		}
		return &domain.StatusError{Status: res.StatusCode, Msg: fmt.Sprintf("Notion %d: %s", res.StatusCode, msg)}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(text, out)
}

func (c *Client) Whoami(ctx context.Context, token string) (string, error) {
	var j struct {
		Name string `json:"name"`
		Bot  struct {
			WorkspaceName string `json:"workspace_name"`
		} `json:"bot"`
	}
	if err := c.api(ctx, token, http.MethodGet, "/users/me", nil, &j); err != nil {
		return "", err
	}
	var parts []string
	for _, s := range []string{j.Name, j.Bot.WorkspaceName} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · "), nil
}

type richText []struct {
	PlainText string `json:"plain_text"`
}

func (r richText) String() string {
	var b strings.Builder
	for _, t := range r {
		b.WriteString(t.PlainText)
	}
	return b.String()
}

type rawPage struct {
	ID             string                     `json:"id"`
	URL            string                     `json:"url"`
	LastEditedTime string                     `json:"last_edited_time"`
	Properties     map[string]json.RawMessage `json:"properties"`
}

func (p rawPage) title() string {
	for _, v := range p.Properties {
		var prop struct {
			Type  string   `json:"type"`
			Title richText `json:"title"`
		}
		if json.Unmarshal(v, &prop) == nil && prop.Type == "title" {
			if t := prop.Title.String(); t != "" {
				return t
			}
			return "(ไม่มีชื่อ)"
		}
	}
	return "(ไม่มีชื่อ)"
}

func (c *Client) Search(ctx context.Context, token, query string, limit int) ([]domain.NotionPage, error) {
	var j struct {
		Results []rawPage `json:"results"`
	}
	body := map[string]any{
		"query": query, "filter": map[string]string{"property": "object", "value": "page"},
		"sort": map[string]string{"direction": "descending", "timestamp": "last_edited_time"}, "page_size": limit,
	}
	if err := c.api(ctx, token, http.MethodPost, "/search", body, &j); err != nil {
		return nil, err
	}
	out := []domain.NotionPage{}
	for _, p := range j.Results {
		out = append(out, domain.NotionPage{ID: p.ID, Title: p.title(), URL: p.URL, EditedAt: p.LastEditedTime})
	}
	return out, nil
}

// PageText reads headings, bullets, to-dos and paragraphs (one level of children).
func (c *Client) PageText(ctx context.Context, token, id string, maxChars int) (domain.PageText, error) {
	if maxChars <= 0 {
		maxChars = 6000
	}
	var page rawPage
	if err := c.api(ctx, token, http.MethodGet, "/pages/"+id, nil, &page); err != nil {
		return domain.PageText{}, err
	}
	var lines []string
	cursor := ""
	for {
		path := "/blocks/" + id + "/children?page_size=100"
		if cursor != "" {
			path += "&start_cursor=" + cursor
		}
		var j struct {
			Results    []map[string]json.RawMessage `json:"results"`
			NextCursor *string                      `json:"next_cursor"`
			HasMore    bool                         `json:"has_more"`
		}
		if err := c.api(ctx, token, http.MethodGet, path, nil, &j); err != nil {
			return domain.PageText{}, err
		}
		for _, b := range j.Results {
			var typ string
			_ = json.Unmarshal(b["type"], &typ)
			var v struct {
				RichText richText `json:"rich_text"`
				Checked  bool     `json:"checked"`
			}
			_ = json.Unmarshal(b[typ], &v)
			t := v.RichText.String()
			switch {
			case strings.HasPrefix(typ, "heading"):
				lines = append(lines, "\n## "+t)
			case typ == "bulleted_list_item" || typ == "numbered_list_item":
				lines = append(lines, "• "+t)
			case typ == "to_do":
				box := "[ ]"
				if v.Checked {
					box = "[x]"
				}
				lines = append(lines, box+" "+t)
			case t != "":
				lines = append(lines, t)
			}
		}
		cursor = ""
		if j.HasMore && j.NextCursor != nil {
			cursor = *j.NextCursor
		}
		if cursor == "" || len([]rune(strings.Join(lines, "\n"))) >= maxChars {
			break
		}
	}
	return domain.PageText{Title: page.title(), URL: page.URL, Text: domain.Truncate(strings.Join(lines, "\n"), maxChars)}, nil
}

func rt(s string) []map[string]any {
	return []map[string]any{{"type": "text", "text": map[string]string{"content": domain.Truncate(s, 1900)}}}
}

var (
	h1Re     = regexp.MustCompile(`^#\s+(.*)`)
	h2Re     = regexp.MustCompile(`^##+\s+(.*)`)
	todoRe   = regexp.MustCompile(`(?i)^\s*\[( |x)\]\s+(.*)`)
	bulletRe = regexp.MustCompile(`^\s*[-•*]\s+(.*)`)
)

// ToBlocks turns light markdown ("# ", "## ", "- " / "• ", "[ ] ", plain lines) into Notion blocks.
func ToBlocks(md string) []map[string]any {
	out := []map[string]any{}
	block := func(typ string, v map[string]any) map[string]any {
		return map[string]any{"object": "block", "type": typ, typ: v}
	}
	for _, raw := range strings.Split(md, "\n") {
		l := strings.TrimRight(raw, " \t\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		if m := h1Re.FindStringSubmatch(l); m != nil {
			out = append(out, block("heading_2", map[string]any{"rich_text": rt(m[1])}))
		} else if m := h2Re.FindStringSubmatch(l); m != nil {
			out = append(out, block("heading_3", map[string]any{"rich_text": rt(m[1])}))
		} else if m := todoRe.FindStringSubmatch(l); m != nil {
			out = append(out, block("to_do", map[string]any{"rich_text": rt(m[2]), "checked": strings.ToLower(m[1]) == "x"}))
		} else if m := bulletRe.FindStringSubmatch(l); m != nil {
			out = append(out, block("bulleted_list_item", map[string]any{"rich_text": rt(m[1])}))
		} else {
			out = append(out, block("paragraph", map[string]any{"rich_text": rt(l)}))
		}
	}
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

func (c *Client) CreatePage(ctx context.Context, token, parentID, title, markdown string) (domain.NotionPage, error) {
	if parentID == "" {
		return domain.NotionPage{}, domain.UserError("ยังไม่ได้ตั้งหน้า Notion ที่จะเก็บเอกสาร (ตั้งค่า > การแจ้งเตือน > หน้า Notion สำหรับเอกสาร)")
	}
	var p rawPage
	body := map[string]any{
		"parent":     map[string]string{"page_id": NormalizeID(parentID)},
		"properties": map[string]any{"title": map[string]any{"title": rt(title)}},
		"children":   ToBlocks(markdown),
	}
	if err := c.api(ctx, token, http.MethodPost, "/pages", body, &p); err != nil {
		return domain.NotionPage{}, err
	}
	return domain.NotionPage{ID: p.ID, Title: title, URL: p.URL, EditedAt: p.LastEditedTime}, nil
}

func (c *Client) Append(ctx context.Context, token, pageID, markdown string) error {
	return c.api(ctx, token, http.MethodPatch, "/blocks/"+NormalizeID(pageID)+"/children", map[string]any{"children": ToBlocks(markdown)}, nil)
}

var (
	idAtEnd = regexp.MustCompile(`(?i)([0-9a-f]{32})(?:\?|$|#)`)
	idAny   = regexp.MustCompile(`(?i)([0-9a-f]{32})`)
)

// NormalizeID accepts a page id or a notion.so URL.
func NormalizeID(s string) string {
	t := strings.ReplaceAll(s, "-", "")
	if m := idAtEnd.FindStringSubmatch(t); m != nil {
		return m[1]
	}
	if m := idAny.FindStringSubmatch(t); m != nil {
		return m[1]
	}
	return s
}
