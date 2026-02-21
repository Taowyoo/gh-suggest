package main

import (
	"os"
	"testing"
)

func TestParsePatchHunksSingle(t *testing.T) {
	data, err := os.ReadFile("testdata/single-hook.diff")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}

	hunks, err := parsePatchHunks(string(data))
	if err != nil {
		t.Fatalf("parsePatchHunks: %v", err)
	}
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}

	hunk := hunks[0]
	if hunk.Path != "main.go" {
		t.Fatalf("expected path main.go, got %q", hunk.Path)
	}
	if hunk.Side != "RIGHT" {
		t.Fatalf("expected side RIGHT, got %q", hunk.Side)
	}
	if hunk.NewStart != 38 {
		t.Fatalf("expected NewStart 38, got %d", hunk.NewStart)
	}
	if len(hunk.NewLines) != 8 {
		t.Fatalf("expected 8 new lines, got %d", len(hunk.NewLines))
	}
}

func TestBuildReviewCommentsSingle(t *testing.T) {
	data, err := os.ReadFile("testdata/single-hook.diff")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}

	hunks, err := parsePatchHunks(string(data))
	if err != nil {
		t.Fatalf("parsePatchHunks: %v", err)
	}

	comments, err := buildReviewComments(hunks)
	if err != nil {
		t.Fatalf("buildReviewComments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}

	comment := comments[0]
	if comment["path"] != "main.go" {
		t.Fatalf("expected path main.go, got %v", comment["path"])
	}
	if comment["line"] != 45 {
		t.Fatalf("expected line 45, got %v", comment["line"])
	}
	if comment["start_line"] != 38 {
		t.Fatalf("expected start_line 38, got %v", comment["start_line"])
	}
	if comment["start_side"] != "RIGHT" {
		t.Fatalf("expected start_side RIGHT, got %v", comment["start_side"])
	}
	if body, ok := comment["body"].(string); !ok || body == "" {
		t.Fatalf("expected non-empty body, got %v", comment["body"])
	}
}

func TestParsePatchHunksMultiple(t *testing.T) {
	data, err := os.ReadFile("testdata/multiple-hooks.diff")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}

	hunks, err := parsePatchHunks(string(data))
	if err != nil {
		t.Fatalf("parsePatchHunks: %v", err)
	}
	if len(hunks) != 4 {
		t.Fatalf("expected 4 hunks, got %d", len(hunks))
	}

	if hunks[0].Path != "main.go" || hunks[0].NewStart != 38 {
		t.Fatalf("unexpected first hunk:\n%s", hunks[0])
	}
	if hunks[0].Side != "RIGHT" {
		t.Fatalf("unexpected first hunk side:\n%s", hunks[0])
	}
	if hunks[1].Path != "main.go" || hunks[1].OldStart != 102 {
		t.Fatalf("unexpected second hunk:\n%s", hunks[1])
	}
	if hunks[1].Side != "RIGHT" {
		t.Fatalf("unexpected second hunk side:\n%s", hunks[1])
	}
	if hunks[2].Path != "main.go" || hunks[2].NewStart != 116 {
		t.Fatalf("unexpected third hunk:\n%s", hunks[2])
	}
	if hunks[2].Side != "RIGHT" {
		t.Fatalf("unexpected third hunk side:\n%s", hunks[2])
	}
	if hunks[3].Path != "main.go" || hunks[3].OldStart != 157 {
		t.Fatalf("unexpected fourth hunk:\n%s", hunks[3])
	}
	if hunks[3].Side != "RIGHT" {
		t.Fatalf("unexpected fourth hunk side:\n%s", hunks[3])
	}
}

func TestParsePatchHunksUnifiedZero(t *testing.T) {
	data, err := os.ReadFile("testdata/multiple-hooks-2.diff")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}

	hunks, err := parsePatchHunks(string(data))
	if err != nil {
		t.Fatalf("parsePatchHunks: %v", err)
	}
	if len(hunks) != 6 {
		t.Fatalf("expected 6 hunks, got %d", len(hunks))
	}

	if hunks[0].Side != "LEFT" || hunks[0].OldStart != 56 {
		t.Fatalf("unexpected first hunk:\n%s", hunks[0])
	}
	if hunks[1].Side != "RIGHT" || hunks[1].NewStart != 81 {
		t.Fatalf("unexpected second hunk:\n%s", hunks[1])
	}
	if hunks[2].Side != "LEFT" || hunks[2].OldStart != 95 {
		t.Fatalf("unexpected third hunk:\n%s", hunks[2])
	}
	if hunks[3].Side != "RIGHT" || hunks[3].NewStart != 195 {
		t.Fatalf("unexpected fourth hunk:\n%s", hunks[3])
	}
	if hunks[4].Side != "RIGHT" || hunks[4].NewStart != 225 {
		t.Fatalf("unexpected fifth hunk:\n%s", hunks[4])
	}
	if hunks[5].Side != "RIGHT" || hunks[5].NewStart != 253 {
		t.Fatalf("unexpected sixth hunk:\n%s", hunks[5])
	}
}
