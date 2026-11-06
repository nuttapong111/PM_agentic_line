package usecase

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// round rounds half up like JavaScript's Math.round.
func round(x float64) int { return int(math.Floor(x + 0.5)) }

func round1(x float64) float64 { return math.Floor(x*10+0.5) / 10 }

func ptr[T any](v T) *T { return &v }

func joinNonEmpty(sep string, xs ...string) string {
	out := xs[:0:0]
	for _, x := range xs {
		if x != "" {
			out = append(out, x)
		}
	}
	return strings.Join(out, sep)
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func itoa(n int) string { return strconv.Itoa(n) }

func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// toJSON marshals a tool result for the model.
func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"result not serialisable"}`
	}
	return string(b)
}
