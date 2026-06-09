package cli

import "testing"

func TestShortID(t *testing.T) {
	const full = "5ee98eef-b09e-4507-85de-4a138b5de284"
	if got := shortID(full, 8); got != "5ee98eef" {
		t.Errorf("shortID(_, 8) = %q, want 5ee98eef", got)
	}
	if got := shortID(full, 12); got != "5ee98eef-b09" {
		t.Errorf("shortID(_, 12) = %q", got)
	}
	if got := shortID(full, 0); got != "5ee98eef" {
		t.Errorf("shortID(_, 0) should default to 8: got %q", got)
	}
	if got := shortID("abc", 8); got != "abc" {
		t.Errorf("shortID shorter than length should return whole: got %q", got)
	}
}
