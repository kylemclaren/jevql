// Package stats accumulates per-query jev metrics.
package stats

import (
	"fmt"
	"time"
)

// Stats is the jev footer data for one query.
type Stats struct {
	CollectRows  int
	Judged       int // distinct (row, question) pairs evaluated
	Requests     int
	CacheHits    int
	InputTokens  int
	OutputTokens int
	USD          float64
	Elapsed      time.Duration
}

// Footer renders: jev: 129 judged, 4 req, 82 cache hits, 21.0k in tokens, $0.0009, 1.02s
func (s Stats) Footer() string {
	return fmt.Sprintf("jev: %d judged, %d req, %d cache hits, %s in tokens, $%.4f, %s",
		s.Judged, s.Requests, s.CacheHits, Compact(s.InputTokens), s.USD, FmtDuration(s.Elapsed))
}

// Compact renders 21000 as 21.0k.
func Compact(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// FmtDuration renders a duration like 1.02s or 340ms.
func FmtDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}
