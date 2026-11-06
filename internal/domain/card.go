package domain

import (
	"strconv"
	"strings"
)

// View models for the cards the bot sends. The LINE adapter turns them into Flex messages.

// Tones of an info card header.
const (
	ToneInfo   = "info"
	ToneWarn   = "warn"
	ToneDanger = "danger"
	ToneOK     = "ok"
)

// Colours of a row, bar or section.
const (
	ColorRed   = "red"
	ColorAmber = "amber"
	ColorGreen = "green"
	ColorMuted = "muted"
)

// Btn is a card button: a postback (Data) or a link (URL).
type Btn struct {
	Label       string
	Data        string
	URL         string
	Primary     bool
	DisplayText string
}

type CardRow struct {
	Label, Value, Color string
}

type CardBar struct {
	Label   string
	Percent int
	Value   string
	Tone    string
}

type CardSection struct {
	Title string
	Lines []string
	Tone  string
}

// InfoCard is a generic card: header, rows, bars, sections, note, buttons.
type InfoCard struct {
	Kicker   string
	Title    string
	Subtitle string
	Tone     string
	Rows     []CardRow
	Bars     []CardBar
	Sections []CardSection
	Note     string
	Buttons  []Btn
}

// Link is a labelled URL on a result card.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// QuickItem is a quick-reply postback button.
type QuickItem struct {
	Label, Data, DisplayText string
}

func itoa(n int) string { return strconv.Itoa(n) }

func joinStr(xs []string, sep string) string { return strings.Join(xs, sep) }

// Num formats a number the way JavaScript prints it (no trailing ".0").
func Num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
