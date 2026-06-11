package ui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Ago renders a compact relative timestamp, k9s-style ("3m", "2h", "5d").
func Ago(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// Bytes renders a human byte size.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for u := n / unit; u >= unit; u /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// Flatten collapses whitespace/newlines so a value fits on one table row.
func Flatten(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// PreviewBody renders a message/value body for display: passes valid UTF-8
// through, and summarises binary content instead of dumping garbage.
func PreviewBody(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return fmt.Sprintf("<binary payload, %s>", Bytes(int64(len(b))))
}
