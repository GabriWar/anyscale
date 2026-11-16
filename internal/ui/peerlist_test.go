package ui

import (
	"testing"
	"time"
)

func TestTrafficSampleRates(t *testing.T) {
	t0 := time.Unix(1000, 0)
	s := trafficSample{}.next(1000, 500, t0)
	if s.rxRate != 0 || s.txRate != 0 {
		t.Fatalf("first sample has rates %v/%v", s.rxRate, s.txRate)
	}
	s = s.next(3000, 1500, t0.Add(2*time.Second))
	if s.rxRate != 1000 || s.txRate != 500 {
		t.Fatalf("rates = %v/%v, want 1000/500", s.rxRate, s.txRate)
	}
	if same := s.next(9999, 9999, t0.Add(2100*time.Millisecond)); same != s {
		t.Fatalf("a sample 100ms later changed the rates to %v/%v", same.rxRate, same.txRate)
	}
	if r := s.next(10, 10, t0.Add(4*time.Second)); r.rxRate != 0 || r.txRate != 0 {
		t.Fatalf("restarted counters gave rates %v/%v", r.rxRate, r.txRate)
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 5 << 20: "5.0 MB", 3 << 30: "3.0 GB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%v) = %q, want %q", n, got, want)
		}
	}
}

func TestJoinInfoSkipsEmpty(t *testing.T) {
	if got := joinInfo("100.64.0.2", "", "linux"); got != "100.64.0.2 · linux" {
		t.Errorf("joinInfo = %q", got)
	}
}
