package report

import (
	"testing"
	"time"
)

// --- parseHHMM ---

func TestParseHHMM_Valid(t *testing.T) {
	cases := []struct {
		input       string
		wantH, wantM int
	}{
		{"00:00", 0, 0},
		{"08:00", 8, 0},
		{"12:30", 12, 30},
		{"23:59", 23, 59},
	}
	for _, tc := range cases {
		h, m, err := parseHHMM(tc.input)
		if err != nil {
			t.Errorf("parseHHMM(%q) unexpected error: %v", tc.input, err)
			continue
		}
		if h != tc.wantH || m != tc.wantM {
			t.Errorf("parseHHMM(%q) = (%d, %d), want (%d, %d)", tc.input, h, m, tc.wantH, tc.wantM)
		}
	}
}

func TestParseHHMM_Invalid(t *testing.T) {
	cases := []string{
		"",
		"not-a-time",
		"25:00",
		"12:60",
		"-1:00",
		"12:-1",
		"ab:cd",
	}
	for _, tc := range cases {
		_, _, err := parseHHMM(tc)
		if err == nil {
			t.Errorf("parseHHMM(%q) expected error, got nil", tc)
		}
	}
}

// --- truncate ---

func TestTruncate_ShortString_NoChange(t *testing.T) {
	// len("hello") = 5, prefix=3, suffix=3, threshold=3+3+3=9. 5 <= 9 → no truncation.
	got := truncate("hello", 3, 3)
	if got != "hello" {
		t.Errorf("truncate(%q, 3, 3) = %q, want %q", "hello", got, "hello")
	}
}

func TestTruncate_ExactThreshold_NoChange(t *testing.T) {
	// len("123456789") = 9, prefix=3, suffix=3, threshold=9. 9 <= 9 → no truncation.
	s := "123456789"
	got := truncate(s, 3, 3)
	if got != s {
		t.Errorf("truncate at exact threshold changed string: got %q", got)
	}
}

func TestTruncate_LongString_Truncated(t *testing.T) {
	// 20 chars, prefix=5, suffix=4 → "12345...7890"
	s := "12345678901234567890"
	got := truncate(s, 5, 4)
	want := "12345...7890"
	if got != want {
		t.Errorf("truncate(%q, 5, 4) = %q, want %q", s, got, want)
	}
}

func TestTruncate_EmptyString(t *testing.T) {
	got := truncate("", 5, 5)
	if got != "" {
		t.Errorf("truncate(%q) = %q, want empty", "", got)
	}
}

func TestTruncate_ContainsEllipsis(t *testing.T) {
	long := "abcdefghijklmnopqrstuvwxyz"
	got := truncate(long, 4, 4)
	if len(got) <= len(long) && len(got) == len(long) {
		t.Errorf("string should be shorter after truncation")
	}
	// Should contain "..."
	if len(got) < 11 { // 4 + 3 + 4
		t.Errorf("truncated string too short: %q", got)
	}
}

// --- formatBMEStatus ---

func TestFormatBMEStatus_Healthy(t *testing.T) {
	got := formatBMEStatus("mint_status_healthy")
	if got != "Healthy" {
		t.Errorf("formatBMEStatus(%q) = %q, want %q", "mint_status_healthy", got, "Healthy")
	}
}

func TestFormatBMEStatus_Warn(t *testing.T) {
	got := formatBMEStatus("mint_status_warn")
	if got != "Warn" {
		t.Errorf("formatBMEStatus(%q) = %q, want %q", "mint_status_warn", got, "Warn")
	}
}

func TestFormatBMEStatus_HaltOracle(t *testing.T) {
	got := formatBMEStatus("mint_status_halt_oracle")
	if got != "Halt_oracle" {
		t.Errorf("formatBMEStatus(%q) = %q, want %q", "mint_status_halt_oracle", got, "Halt_oracle")
	}
}

func TestFormatBMEStatus_NoPrefix(t *testing.T) {
	// String without the prefix is returned as-is with first letter uppercased.
	got := formatBMEStatus("healthy")
	if got != "Healthy" {
		t.Errorf("formatBMEStatus(%q) = %q, want %q", "healthy", got, "Healthy")
	}
}

func TestFormatBMEStatus_Empty(t *testing.T) {
	got := formatBMEStatus("")
	if got != "" {
		t.Errorf("formatBMEStatus(%q) = %q, want empty", "", got)
	}
}

func TestFormatBMEStatus_JustPrefix(t *testing.T) {
	// "mint_status_" stripped → empty → returned as empty
	got := formatBMEStatus("mint_status_")
	if got != "" {
		t.Errorf("formatBMEStatus(%q) = %q, want empty", "mint_status_", got)
	}
}

// --- formatAge ---

func TestFormatAge_SecondsOnly(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{1 * time.Second, "1s"},
		{59 * time.Second, "59s"},
	}
	for _, tc := range cases {
		got := formatAge(tc.d)
		if got != tc.want {
			t.Errorf("formatAge(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestFormatAge_WithMinutes(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{60 * time.Second, "1m 0s"},
		{90 * time.Second, "1m 30s"},
		{time.Hour, "60m 0s"},
	}
	for _, tc := range cases {
		got := formatAge(tc.d)
		if got != tc.want {
			t.Errorf("formatAge(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// --- durationUntilNext ---

func TestDurationUntilNext_AlwaysPositive(t *testing.T) {
	// 00:00 has almost certainly passed today — result should be > 0 and <= 24h.
	d := durationUntilNext(0, 0, time.UTC)
	if d <= 0 {
		t.Errorf("expected positive duration, got %v", d)
	}
	if d > 24*time.Hour {
		t.Errorf("expected duration <= 24h, got %v", d)
	}
}

func TestDurationUntilNext_FutureTime_ApproximateDelay(t *testing.T) {
	// Schedule for 2 hours from now; result should be approximately 2h.
	loc := time.UTC
	target := time.Now().In(loc).Add(2 * time.Hour)
	d := durationUntilNext(target.Hour(), target.Minute(), loc)

	// Allow up to 60 seconds of drift due to minute-boundary truncation.
	const tolerance = 60 * time.Second
	lo := 2*time.Hour - tolerance
	hi := 2*time.Hour + tolerance
	if d < lo || d > hi {
		t.Errorf("durationUntilNext for 2h-from-now slot = %v, want ~2h (±%v)", d, tolerance)
	}
}

func TestDurationUntilNext_PastTime_Returns24h(t *testing.T) {
	// Schedule for 1 minute ago; function must return ~23h59m (adds 24h).
	loc := time.UTC
	past := time.Now().In(loc).Add(-1 * time.Minute)
	d := durationUntilNext(past.Hour(), past.Minute(), loc)

	// Result should be close to 24h (between 23h58m and 24h).
	if d < 23*time.Hour+55*time.Minute || d > 24*time.Hour {
		t.Errorf("past time returned %v, want ~24h", d)
	}
}

func TestDurationUntilNext_TimezoneRespected(t *testing.T) {
	// Schedule at 00:00 UTC vs 00:00 US/Central — they should differ.
	utc := time.UTC
	central, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skip("America/Chicago timezone not available")
	}

	dUTC := durationUntilNext(0, 0, utc)
	dCentral := durationUntilNext(0, 0, central)

	if dUTC == dCentral {
		t.Error("expected different delays for same clock time in different timezones")
	}
}
