// Package config reads every setting from environment variables. See .env.example.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	Port      int
	PublicURL string
	Timezone  string
	DataDir   string

	LineChannelSecret string
	LineAccessToken   string
	// Only this LINE user may use the bot. Empty on first run: the bot replies with the user ID.
	OwnerUserID string

	ClaudeModel         string
	ClaudeEffort        string
	ClaudeMaxToolRounds int
	AnthropicAPIKey     string

	// Plane values are only the starting point: the Settings page can replace them.
	PlaneBaseURL       string
	PlaneAPIKey        string
	PlaneWorkspace     string
	PlaneProjects      []string
	Nicknames          map[string]string
	PlaneWebhookSecret string
	StaleDays          int
	BugLabel           string

	DigestCron     string
	DashboardToken string
	// EncryptionKey protects secrets saved from the Settings page. Falls back to DashboardToken.
	EncryptionKey string
	DatabaseURL   string
	RichMenuAuto  bool
}

var placeholder = regexp.MustCompile(`(?i)^PASTE_HERE$`)

// IsSet reports a value that is present and not the "PASTE_HERE" placeholder.
func IsSet(v string) bool { return v != "" && !placeholder.MatchString(v) }

// EnvPlaneSet reports whether the PLANE_* env vars describe a usable connection.
func (c Config) EnvPlaneSet() bool {
	return IsSet(c.PlaneBaseURL) && IsSet(c.PlaneAPIKey) && IsSet(c.PlaneWorkspace)
}

// Load reads the environment.
func Load() (Config, error) { return LoadFrom(os.Getenv) }

// LoadFrom reads settings through getenv (tests pass a map lookup).
func LoadFrom(getenv func(string) string) (Config, error) {
	var errs []string
	req := func(name string) string {
		v := getenv(name)
		if v == "" {
			errs = append(errs, "Missing required env var "+name)
		}
		return v
	}
	opt := func(name, def string) string {
		if v := getenv(name); v != "" {
			return v
		}
		return def
	}
	num := func(name, def string) int {
		n, err := strconv.Atoi(opt(name, def))
		if err != nil {
			errs = append(errs, name+" must be a number")
		}
		return n
	}
	c := Config{
		Port:                num("PORT", "3000"),
		PublicURL:           strings.TrimSuffix(opt("PUBLIC_URL", "http://localhost:3000"), "/"),
		Timezone:            opt("TZ_NAME", "Asia/Bangkok"),
		DataDir:             opt("DATA_DIR", "./data"),
		LineChannelSecret:   req("LINE_CHANNEL_SECRET"),
		LineAccessToken:     req("LINE_CHANNEL_ACCESS_TOKEN"),
		OwnerUserID:         opt("OWNER_LINE_USER_ID", ""),
		ClaudeModel:         opt("CLAUDE_MODEL", "claude-haiku-5-5"),
		ClaudeEffort:        opt("CLAUDE_EFFORT", "low"),
		ClaudeMaxToolRounds: num("CLAUDE_MAX_TOOL_ROUNDS", "8"),
		AnthropicAPIKey:     opt("ANTHROPIC_API_KEY", ""),
		PlaneBaseURL:        strings.TrimSuffix(opt("PLANE_BASE_URL", ""), "/"),
		PlaneAPIKey:         opt("PLANE_API_KEY", ""),
		PlaneWorkspace:      opt("PLANE_WORKSPACE_SLUG", ""),
		PlaneWebhookSecret:  opt("PLANE_WEBHOOK_SECRET", ""),
		StaleDays:           num("STALE_DAYS", "5"),
		BugLabel:            opt("BUG_LABEL", "bug"),
		DigestCron:          opt("DIGEST_CRON", "30 8 * * 1-5"),
		DashboardToken:      req("DASHBOARD_TOKEN"),
		DatabaseURL:         opt("DATABASE_URL", ""),
		RichMenuAuto:        getenv("RICHMENU_AUTO") != "false",
	}
	c.EncryptionKey = opt("ENCRYPTION_KEY", getenv("DASHBOARD_TOKEN"))
	c.PlaneProjects = []string{}
	c.Nicknames = map[string]string{}
	if raw := getenv("PLANE_PROJECTS"); raw != "" {
		if json.Unmarshal([]byte(raw), &c.PlaneProjects) != nil {
			errs = append(errs, "PLANE_PROJECTS must be valid JSON")
		}
	}
	if raw := getenv("NICKNAMES"); raw != "" {
		if json.Unmarshal([]byte(raw), &c.Nicknames) != nil {
			errs = append(errs, "NICKNAMES must be valid JSON")
		}
	}
	if len(errs) > 0 {
		return c, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return c, nil
}
