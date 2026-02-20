package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"
)

func main() {
	repoArg := ""
	prNumber := 0

	flag.StringVar(&repoArg, "repo", "", "Select another repository using the [HOST/]OWNER/REPO format")
	flag.StringVar(&repoArg, "R", "", "Select another repository using the [HOST/]OWNER/REPO format")
	flag.IntVar(&prNumber, "pr", 0, "Pull request number")
	flag.Parse()

	if prNumber <= 0 {
		fatalf("missing required --pr PR_NUM")
	}

	args := flag.Args()
	if len(args) != 1 {
		fatalf("missing required patch file path")
	}
	patchPath := args[0]

	repo, err := resolveRepo(repoArg)
	if err != nil {
		fatalf("resolve repo: %v", err)
	}

	patchBytes, err := os.ReadFile(patchPath)
	if err != nil {
		fatalf("read patch: %v", err)
	}

	patch, err := parseSingleHunkPatch(string(patchBytes))
	if err != nil {
		fatalf("parse patch: %v", err)
	}

	if patch.NewCount == 0 {
		fatalf("patch has no added/modified lines; cannot create a suggestion")
	}

	client, err := api.NewRESTClient(api.ClientOptions{Host: repo.Host})
	if err != nil {
		fatalf("api client: %v", err)
	}

	prInfo, err := fetchPullRequest(client, repo, prNumber)
	if err != nil {
		fatalf("fetch PR: %v", err)
	}

	body := buildSuggestionBody(patch.NewLines)
	if err := createReviewWithComment(client, repo, prNumber, prInfo.HeadSHA, patch, body); err != nil {
		fatalf("create review: %v", err)
	}

	fmt.Printf("created review suggestion on %s#%d\n", repoFullName(repo), prNumber)
}

// For more examples of using go-gh, see:
// https://github.com/cli/go-gh/blob/trunk/example_gh_test.go

type patchHunk struct {
	Path     string
	NewStart int
	NewCount int
	NewLines []string
}

func resolveRepo(repoArg string) (repository.Repository, error) {
	if repoArg != "" {
		return repository.Parse(repoArg)
	}
	return repository.Current()
}

func parseSingleHunkPatch(input string) (patchHunk, error) {
	var (
		currentPath string
		hunks       []patchHunk
	)

	lines := strings.Split(input, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "diff --git ") {
			path, err := parseDiffPath(line)
			if err != nil {
				return patchHunk{}, err
			}
			currentPath = path
			continue
		}

		if strings.HasPrefix(line, "@@ ") {
			if currentPath == "" {
				return patchHunk{}, errors.New("hunk without file header")
			}
			newStart, newCount, err := parseHunkHeader(line)
			if err != nil {
				return patchHunk{}, err
			}

			hunkLines := []string{}
			for j := i + 1; j < len(lines); j++ {
				next := lines[j]
				if strings.HasPrefix(next, "diff --git ") || strings.HasPrefix(next, "@@ ") {
					i = j - 1
					break
				}
				if strings.HasPrefix(next, "\\ No newline at end of file") {
					continue
				}
				if len(next) == 0 {
					hunkLines = append(hunkLines, "")
					continue
				}
				prefix := next[0]
				if prefix != ' ' && prefix != '+' && prefix != '-' {
					continue
				}
				if prefix == ' ' || prefix == '+' {
					hunkLines = append(hunkLines, next[1:])
				}
				if j == len(lines)-1 {
					i = j
				}
			}

			hunks = append(hunks, patchHunk{
				Path:     currentPath,
				NewStart: newStart,
				NewCount: newCount,
				NewLines: hunkLines,
			})
		}
	}

	if len(hunks) == 0 {
		return patchHunk{}, errors.New("no hunks found")
	}
	if len(hunks) > 1 {
		return patchHunk{}, errors.New("patch contains multiple hunks; only single-hunk patches are supported")
	}

	return hunks[0], nil
}

func parseDiffPath(line string) (string, error) {
	// Format: diff --git a/path b/path
	parts := strings.Split(line, " b/")
	if len(parts) != 2 {
		return "", fmt.Errorf("unexpected diff header: %q", line)
	}
	return strings.TrimSpace(parts[1]), nil
}

func parseHunkHeader(line string) (int, int, error) {
	// Format: @@ -oldStart,oldCount +newStart,newCount @@
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return 0, 0, fmt.Errorf("unexpected hunk header: %q", line)
	}
	newRange := fields[2]
	if !strings.HasPrefix(newRange, "+") {
		return 0, 0, fmt.Errorf("unexpected hunk header: %q", line)
	}
	newRange = strings.TrimPrefix(newRange, "+")
	parts := strings.SplitN(newRange, ",", 2)
	start, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid hunk start: %q", line)
	}
	count := 1
	if len(parts) == 2 {
		count, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid hunk count: %q", line)
		}
	}
	return start, count, nil
}

type pullInfo struct {
	HeadSHA string
}

func fetchPullRequest(client *api.RESTClient, repo repository.Repository, prNumber int) (pullInfo, error) {
	var resp struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	path := fmt.Sprintf("repos/%s/%s/pulls/%d", repo.Owner, repo.Name, prNumber)
	if err := client.Get(path, &resp); err != nil {
		return pullInfo{}, err
	}
	if resp.Head.SHA == "" {
		return pullInfo{}, errors.New("missing PR head SHA")
	}
	return pullInfo{HeadSHA: resp.Head.SHA}, nil
}

func buildSuggestionBody(lines []string) string {
	return "```suggestion\n" + strings.Join(lines, "\n") + "\n```"
}

func createReviewWithComment(client *api.RESTClient, repo repository.Repository, prNumber int, headSHA string, hunk patchHunk, body string) error {
	endLine := hunk.NewStart + hunk.NewCount - 1
	comments := []map[string]interface{}{
		{
			"path": hunk.Path,
			"side": "RIGHT",
			"line": endLine,
			"body": body,
		},
	}
	if hunk.NewCount > 1 {
		comments[0]["start_side"] = "RIGHT"
		comments[0]["start_line"] = hunk.NewStart
	}

	payload := map[string]interface{}{
		"commit_id": headSHA,
		"event":     "COMMENT",
		"comments":  comments,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", repo.Owner, repo.Name, prNumber)
	return client.Post(path, strings.NewReader(string(bodyBytes)), nil)
}

func repoFullName(repo repository.Repository) string {
	if repo.Host != "" && !strings.EqualFold(repo.Host, "github.com") {
		return fmt.Sprintf("%s/%s/%s", repo.Host, repo.Owner, repo.Name)
	}
	return fmt.Sprintf("%s/%s", repo.Owner, repo.Name)
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
