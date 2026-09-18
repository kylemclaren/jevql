package serve

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// rateLimiter is a per-client token bucket keyed by IP (X-Forwarded-For
// aware, since nodes usually sit behind a proxy that terminates TLS).
type rateLimiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   float64
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(perMinute int) *rateLimiter {
	return &rateLimiter{rate: float64(perMinute) / 60, burst: float64(perMinute), buckets: map[string]*bucket{}}
}

func clientIP(r *http.Request) string {
	if xf := r.Header.Get("Fly-Client-IP"); xf != "" {
		return xf
	}
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		if i := indexByte(xf, ','); i >= 0 {
			return trim(xf[:i])
		}
		return trim(xf)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
		if len(l.buckets) > 10000 { // crude bound; drop everything and start over
			l.buckets = map[string]*bucket{key: b}
		}
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *rateLimiter) middleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || r.URL.Path == "/openapi.json" {
			h.ServeHTTP(w, r)
			return
		}
		if !l.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "10")
			writeJSON(w, http.StatusTooManyRequests, struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}{"rate limit exceeded; slow down", "rate"})
			return
		}
		h.ServeHTTP(w, r)
	})
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func trim(s string) string {
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}
