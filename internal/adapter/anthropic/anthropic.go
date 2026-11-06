// Package anthropic calls the Claude Messages API over plain HTTP, so conversation content
// blocks are stored and sent back exactly as the API returns them.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

type Client struct {
	http *http.Client
	Base string
}

func New(hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 120 * time.Second}
	}
	return &Client{http: hc, Base: "https://api.anthropic.com/v1"}
}

func (c *Client) do(ctx context.Context, apiKey, method, path string, body any, out any) error {
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
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	text, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		msg := string(text)
		var j struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(text, &j) == nil && j.Error.Message != "" {
			msg = j.Error.Message
		}
		return &domain.StatusError{Status: res.StatusCode, Msg: fmt.Sprintf("%d %s", res.StatusCode, domain.Truncate(msg, 300))}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(text, out)
}

func (c *Client) Create(ctx context.Context, apiKey string, r port.LLMRequest) (port.LLMResponse, error) {
	body := map[string]any{
		"model": r.Model, "max_tokens": r.MaxTokens, "system": r.System, "tools": r.Tools, "messages": r.Messages,
	}
	if r.Effort != "" {
		body["output_config"] = map[string]string{"effort": r.Effort}
	}
	var out port.LLMResponse
	err := c.do(ctx, apiKey, http.MethodPost, "/messages", body, &out)
	return out, err
}

func (c *Client) CheckModel(ctx context.Context, apiKey, model string) error {
	return c.do(ctx, apiKey, http.MethodGet, "/models/"+url.PathEscape(model), nil, nil)
}
