package api

import (
	"testing"
	"time"
)

func TestIPLimiter(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, l *ipLimiter)
	}{
		{"burst is spent then refused", func(t *testing.T, l *ipLimiter) {
			for i := range 3 {
				if ok, _ := l.allow("a", t0); !ok {
					t.Fatalf("call %d refused within burst", i)
				}
			}
			ok, delay := l.allow("a", t0)
			if ok || delay <= 0 {
				t.Fatalf("over-burst call: ok=%v delay=%v", ok, delay)
			}
		}},
		{"ips have separate budgets", func(t *testing.T, l *ipLimiter) {
			for range 3 {
				l.allow("a", t0)
			}
			if ok, _ := l.allow("b", t0); !ok {
				t.Fatal("second IP refused")
			}
		}},
		{"budget refills over time", func(t *testing.T, l *ipLimiter) {
			for range 3 {
				l.allow("a", t0)
			}
			if ok, _ := l.allow("a", t0.Add(time.Minute)); !ok {
				t.Fatal("still refused after refill period")
			}
		}},
		{"swept entry comes back fresh", func(t *testing.T, l *ipLimiter) {
			for range 3 {
				l.allow("a", t0)
			}
			l.allow("b", t0.Add(limiterIdleTTL))
			l.sweep(t0.Add(limiterIdleTTL + time.Second))
			if _, ok := l.entries["a"]; ok {
				t.Fatal("idle entry not swept")
			}
			if _, ok := l.entries["b"]; !ok {
				t.Fatal("recent entry swept")
			}
			if ok, _ := l.allow("a", t0.Add(limiterIdleTTL+time.Second)); !ok {
				t.Fatal("swept IP refused")
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, newIPLimiter(RateLimit{PerMinute: 1, Burst: 3}))
		})
	}
}
