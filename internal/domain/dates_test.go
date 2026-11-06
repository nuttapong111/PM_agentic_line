package domain

import (
	"strings"
	"testing"
	"time"
)

func TestLocalTimes(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Bangkok")
	got := LocalToTime("2026-11-12", "10:00", loc).UTC().Format(time.RFC3339)
	if got != "2026-11-12T03:00:00Z" {
		t.Errorf("Bangkok 10:00 = %s", got)
	}
	if s := LocalISO(time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC), loc); s != "2026-11-12T10:00:00+07:00" {
		t.Errorf("LocalISO = %s", s)
	}
	if !InQuietHours("20:00", "08:00", "23:10") || InQuietHours("20:00", "08:00", "09:00") {
		t.Error("quiet hours across midnight")
	}
	if d := DaysBetween("2026-11-01", "2026-11-08"); d != 7 {
		t.Errorf("DaysBetween = %d", d)
	}
	if w := Weekday("2026-11-06"); w != 5 {
		t.Errorf("Weekday = %d", w)
	}
}

func TestTextHelpers(t *testing.T) {
	if h := TextToHTML("สรุป\n\n- a\n- b"); !strings.Contains(h, "<li>a</li>") {
		t.Errorf("TextToHTML = %s", h)
	}
	if !IsEmail("a@b.co") || IsEmail("bad") {
		t.Error("IsEmail")
	}
}
