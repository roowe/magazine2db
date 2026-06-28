package main

import "testing"

func TestRemoveBlankLines(t *testing.T) {
	input := "first paragraph\n\n   \nsecond paragraph\nthird line"
	want := "first paragraph\nsecond paragraph\nthird line"
	if got := removeBlankLines(input); got != want {
		t.Fatalf("removeBlankLines() = %q, want %q", got, want)
	}
}
