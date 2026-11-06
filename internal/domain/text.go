package domain

import (
	"regexp"
	"strings"
)

var normRe = regexp.MustCompile(`[\s_\-:]+`)

// Norm lowercases and strips spaces, dashes, underscores and colons for fuzzy matching.
func Norm(s string) string { return normRe.ReplaceAllString(strings.ToLower(s), "") }

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

// EscapeHTML escapes text for HTML bodies.
func EscapeHTML(s string) string { return htmlEscaper.Replace(s) }

var (
	blankLines = regexp.MustCompile(`\n{2,}`)
	bulletLine = regexp.MustCompile(`^\s*[-•]\s+`)
)

// TextToHTML turns plain text (with "- " bullets and blank lines) into a simple HTML email body.
func TextToHTML(body string) string {
	var b strings.Builder
	b.WriteString(`<div style="font-family:Sarabun,Tahoma,sans-serif;font-size:14px;color:#18202B">`)
	for _, block := range blankLines.Split(strings.TrimSpace(body), -1) {
		lines := strings.Split(block, "\n")
		allBullets := true
		for _, l := range lines {
			if !bulletLine.MatchString(l) {
				allBullets = false
				break
			}
		}
		if allBullets {
			b.WriteString("<ul>")
			for _, l := range lines {
				b.WriteString("<li>" + EscapeHTML(bulletLine.ReplaceAllString(l, "")) + "</li>")
			}
			b.WriteString("</ul>")
			continue
		}
		esc := make([]string, len(lines))
		for i, l := range lines {
			esc[i] = EscapeHTML(l)
		}
		b.WriteString("<p>" + strings.Join(esc, "<br>") + "</p>")
	}
	b.WriteString("</div>")
	return b.String()
}

// Truncate cuts s to at most n runes.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// IsEmail is a loose email check.
func IsEmail(s string) bool { return emailRe.MatchString(s) }

var blockRe = regexp.MustCompile(`(?i)block`)

// IsBlockedName reports a state whose name says "blocked".
func IsBlockedName(name string) bool { return blockRe.MatchString(name) }

// Uniq keeps the first occurrence of each string.
func Uniq(xs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
