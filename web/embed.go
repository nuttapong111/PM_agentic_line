// Package web holds the static pages and the rich menu image, embedded into the binary.
package web

import _ "embed"

//go:embed dashboard.html
var DashboardHTML []byte

//go:embed settings.html
var SettingsHTML []byte

//go:embed richmenu.png
var RichMenuPNG []byte
