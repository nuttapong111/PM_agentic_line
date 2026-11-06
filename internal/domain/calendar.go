package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// CalEvent is a calendar event, normalised from Google Calendar.
type CalEvent struct {
	ID          string   `json:"id"`
	Summary     string   `json:"summary"`
	Start       string   `json:"start"`
	End         string   `json:"end"`
	AllDay      bool     `json:"allDay"`
	MeetURL     string   `json:"meetUrl,omitempty"`
	HTMLLink    string   `json:"htmlLink,omitempty"`
	Attendees   []string `json:"attendees"`
	Description string   `json:"description,omitempty"`
}

// StartTime parses the event start.
func (e CalEvent) StartTime() time.Time { t, _ := ParseInstant(e.Start); return t }

// EndTime parses the event end.
func (e CalEvent) EndTime() time.Time { t, _ := ParseInstant(e.End); return t }

// Interval is a busy period.
type Interval struct{ Start, End time.Time }

// Slot is a candidate meeting time.
type Slot struct {
	Date     string   `json:"date"`
	Start    string   `json:"start"`
	End      string   `json:"end"`
	StartISO string   `json:"startIso"`
	EndISO   string   `json:"endIso"`
	Free     int      `json:"free"`
	Total    int      `json:"total"`
	Missing  []string `json:"missing"`
}

func hhmm(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

// FindSlots lists candidate slots in working hours (09:00–18:00, Mon–Fri, no lunch overlap,
// at least 30 minutes from now), fewest people missing first. known keeps the attendee order.
func FindSlots(known []string, busy map[string][]Interval, fromDate string, days, minutes int, now time.Time, loc *time.Location) []Slot {
	start := LocalToTime(fromDate, "00:00", loc)
	slots := []Slot{}
	for d := 0; d < days; d++ {
		date := DateIn(start.Add(time.Duration(d)*24*time.Hour+12*time.Hour), loc)
		if w := Weekday(date); w == 0 || w == 6 {
			continue
		}
		for m := 9 * 60; m+minutes <= 18*60; m += 30 {
			if m < 13*60 && m+minutes > 12*60 {
				continue // skip lunch
			}
			s := LocalToTime(date, hhmm(m), loc)
			e := s.Add(time.Duration(minutes) * time.Minute)
			if s.Before(now.Add(30 * time.Minute)) {
				continue
			}
			missing := []string{}
			for _, who := range known {
				for _, b := range busy[who] {
					if b.Start.Before(e) && b.End.After(s) {
						missing = append(missing, who)
						break
					}
				}
			}
			slots = append(slots, Slot{
				Date: date, Start: hhmm(m), End: hhmm(m + minutes), StartISO: LocalISO(s, loc), EndISO: LocalISO(e, loc),
				Free: len(known) - len(missing), Total: len(known), Missing: missing,
			})
		}
	}
	sort.SliceStable(slots, func(i, j int) bool {
		a, b := slots[i], slots[j]
		if len(a.Missing) != len(b.Missing) {
			return len(a.Missing) < len(b.Missing)
		}
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		return a.Start < b.Start
	})
	return slots
}

// Person is someone whose leave is looked up by name aliases.
type Person struct {
	ID      string
	Name    string
	Aliases []string
}

// LeaveDays maps each person to the dates in [from, to] covered by leave events that mention them.
func LeaveDays(events []CalEvent, people []Person, from, to string) map[string][]string {
	out := map[string][]string{}
	for _, p := range people {
		var keys []string
		for _, a := range p.Aliases {
			if a != "" {
				keys = append(keys, strings.ToLower(a))
			}
		}
		for _, e := range events {
			hay := strings.ToLower(e.Summary + " " + strings.Join(e.Attendees, " "))
			hit := false
			for _, k := range keys {
				if strings.Contains(hay, k) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
			s := first10(e.Start)
			if !ValidDate(s) {
				continue
			}
			en := first10(e.End)
			if e.AllDay {
				en = AddDays(e.End, -1)
			}
			for d := s; d <= en && d <= to; d = AddDays(d, 1) {
				if d >= from && !contains(out[p.Name], d) {
					out[p.Name] = append(out[p.Name], d)
				}
			}
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// NotionPage is a search hit or a created page.
type NotionPage struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	EditedAt string `json:"editedAt"`
}

// PageText is a Notion page read as plain text.
type PageText struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Text  string `json:"text"`
}
