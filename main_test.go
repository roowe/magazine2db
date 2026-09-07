package main

import (
	"context"
	"strings"
	"testing"
)

func TestRemoveBlankLines(t *testing.T) {
	input := "first paragraph\n\n   \nsecond paragraph\nthird line"
	want := "first paragraph\nsecond paragraph\nthird line"
	if got := removeBlankLines(input); got != want {
		t.Fatalf("removeBlankLines() = %q, want %q", got, want)
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	if err := run(context.Background(), []string{"unknown"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("expected unknown command error, got %v", err)
	}
}
