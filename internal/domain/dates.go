package domain

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Dates are passed around as "YYYY-MM-DD" strings in the configured timezone, like the
// task tools return them. These helpers do calendar arithmetic on those strings.

const ymd = "2006-01-02"

// DateIn formats an instant as a local calendar date.
func DateIn(t time.Time, loc *time.Location) string { return t.In(loc).Format(ymd) }

func parseYMD(s string) (time.Time, bool) {
	if len(s) < 10 {
		return time.Time{}, false
	}
	t, err := time.Parse(ymd, s[:10])
	return t, err == nil
}

// DaysBetween counts calendar days from a to b (both "YYYY-MM-DD…").
func DaysBetween(a, b string) int {
	ta, ok1 := parseYMD(a)
	tb, ok2 := parseYMD(b)
	if !ok1 || !ok2 {
		return 0
	}
	return int(math.Round(tb.Sub(ta).Hours() / 24))
}

// AddDays shifts a date by n days.
func AddDays(date string, n int) string {
	t, ok := parseYMD(date)
	if !ok {
		return date
	}
	return t.AddDate(0, 0, n).Format(ymd)
}

// Weekday of a date, 0 = Sunday.
func Weekday(date string) int {
	t, ok := parseYMD(date)
	if !ok {
		return 0
	}
	return int(t.Weekday())
}

// StartOfWeek returns the Monday of the date's week.
func StartOfWeek(date string) string {
	dow := (Weekday(date) + 6) % 7
	return AddDays(date, -dow)
}

// ISOWeek returns the ISO-8601 week number.
func ISOWeek(date string) int {
	t, _ := parseYMD(date)
	_, w := t.ISOWeek()
	return w
}

// ParseInstant parses the timestamps APIs return (RFC 3339 with or without fraction).
func ParseInstant(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", ymd} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

var thMonths = []string{"ม.ค.", "ก.พ.", "มี.ค.", "เม.ย.", "พ.ค.", "มิ.ย.", "ก.ค.", "ส.ค.", "ก.ย.", "ต.ค.", "พ.ย.", "ธ.ค."}
var thDays = []string{"อา.", "จ.", "อ.", "พ.", "พฤ.", "ศ.", "ส."}

// ThaiDaysFull are weekday names, Sunday first.
var ThaiDaysFull = []string{"วันอาทิตย์", "วันจันทร์", "วันอังคาร", "วันพุธ", "วันพฤหัสบดี", "วันศุกร์", "วันเสาร์"}

// ThaiDate renders "ศ. 7 พ.ย." (or "7 พ.ย." without the weekday). Empty input gives "-".
func ThaiDate(date string, withDay bool) string {
	t, ok := parseYMD(date)
	if !ok {
		return "-"
	}
	s := fmt.Sprintf("%d %s", t.Day(), thMonths[t.Month()-1])
	if withDay {
		return thDays[t.Weekday()] + " " + s
	}
	return s
}

// ThaiDatePtr is ThaiDate for optional dates.
func ThaiDatePtr(date *string, withDay bool) string {
	if date == nil {
		return "-"
	}
	return ThaiDate(*date, withDay)
}

// HM formats local "HH:MM".
func HM(t time.Time, loc *time.Location) string { return t.In(loc).Format("15:04") }

// LocalToTime turns a local date and "HH:MM" into an instant.
func LocalToTime(date, hm string, loc *time.Location) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", date[:min(10, len(date))]+" "+padHM(hm), loc)
	if err != nil {
		return time.Time{}
	}
	return t
}

func padHM(hm string) string {
	if len(hm) == 4 && strings.Index(hm, ":") == 1 {
		return "0" + hm
	}
	return hm
}

// ParseLocal accepts "2026-11-03T10:00", "2026-11-03 10:00" (local) or a full RFC 3339 string.
func ParseLocal(s string, loc *time.Location) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T3:04", "2006-01-02 3:04"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, true
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// LocalISO renders an instant with the local offset, e.g. 2026-11-12T10:00:00+07:00.
func LocalISO(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("2006-01-02T15:04:05-07:00")
}

// InQuietHours reports whether now ("HH:MM") falls in [from, to), wrapping midnight.
func InQuietHours(from, to, now string) bool {
	if from == "" || to == "" || from == to {
		return false
	}
	if from < to {
		return now >= from && now < to
	}
	return now >= from || now < to
}

// ValidDate reports whether s is "YYYY-MM-DD".
func ValidDate(s string) bool {
	if len(s) != 10 {
		return false
	}
	_, err := time.Parse(ymd, s)
	return err == nil
}

// CheckDate returns a user error when an optional date is malformed.
func CheckDate(d *string, field string) error {
	if d == nil {
		return nil
	}
	if !ValidDate(*d) {
		return UserError(fmt.Sprintf(`%s ต้องเป็น YYYY-MM-DD (ได้ "%s")`, field, *d))
	}
	return nil
}
