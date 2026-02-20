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
	if hunks[1].Path != "main.go" || hunks[1].NewStart != 102 {
		t.Fatalf("unexpected second hunk:\n%s", hunks[1])
	}
	if hunks[2].Path != "main.go" || hunks[2].NewStart != 116 {
		t.Fatalf("unexpected third hunk:\n%s", hunks[2])
	}
	if hunks[3].Path != "main.go" || hunks[3].NewStart != 155 {
		t.Fatalf("unexpected fourth hunk:\n%s", hunks[3])
	}
}
