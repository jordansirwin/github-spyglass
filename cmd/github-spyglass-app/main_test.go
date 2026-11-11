package main

import (
	"testing"
	"time"
)

func TestFormatEventTime(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, 11, 11, 9, 30, 0, 0, loc)
	tests := []struct {
		name string
		when time.Time
		want string
	}{
		{"today", time.Date(2026, 11, 11, 7, 5, 0, 0, loc), "07:05"},
		{"just after midnight today", time.Date(2026, 11, 11, 0, 1, 0, 0, loc), "00:01"},
		{"yesterday", time.Date(2026, 11, 10, 23, 59, 0, 0, loc), "Nov 10 23:59"},
		{"earlier this year", time.Date(2026, 3, 2, 14, 25, 0, 0, loc), "Mar 2 14:25"},
		{"previous year", time.Date(2025, 12, 31, 8, 0, 0, 0, loc), "Dec 31 2025 08:00"},
		{"same yearday, different year", time.Date(2025, 11, 11, 8, 0, 0, 0, loc), "Nov 11 2025 08:00"},
	}
	for _, tt := range tests {
		if got := formatEventTime(tt.when, now); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}
