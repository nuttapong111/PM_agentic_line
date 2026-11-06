// Command richmenu installs the 6-button rich menu as the LINE OA default (replacing ours if present).
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/adapter/line"
	"github.com/nuttapong111/pm_agentic_line/internal/config"
	"github.com/nuttapong111/pm_agentic_line/web"
)

func main() {
	config.LoadDotEnv(".env")
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	dash := cfg.PublicURL + "/dashboard?" + url.Values{"t": {cfg.DashboardToken}}.Encode()
	c := line.New(cfg.LineAccessToken, &http.Client{Timeout: 60 * time.Second})
	id, err := c.EnsureRichMenu(context.Background(), dash, web.RichMenuPNG, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("rich menu set:", id)
}
