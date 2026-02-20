package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"
)

func main() {
	repoArg := ""
	prNumber := 0
	dryRun := false

	flag.StringVar(&repoArg, "repo", "", "Select another repository using the [HOST/]OWNER/REPO format")
	flag.StringVar(&repoArg, "R", "", "Select another repository using the [HOST/]OWNER/REPO format")
	flag.IntVar(&prNumber, "pr", 0, "Pull request number")
	flag.BoolVar(&dryRun, "dry-run", false, "Print suggestion comments to stdout instead of posting to GitHub")
	flag.BoolVar(&dryRun, "d", false, "Print suggestion comments to stdout instead of posting to GitHub")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage:
  gh suggest [--pr PR_NUM] [-R|--repo [HOST/]OWNER/REPO] [--dry-run] [PATCH_FILE]

Summary:
  Convert a git diff patch into a single GitHub PR review suggestion comment.

Arguments:
  PATCH_FILE  Path to a unified diff file. Use "-" to read from stdin.

Required:
  --pr PR_NUM  Pull request number.

Options:
  -R, --repo [HOST/]OWNER/REPO  Select another repository.
  -d, --dry-run                Print suggestion comments to stdout instead of posting to GitHub.

Notes:
  - The patch may contain multiple hunks; each hunk becomes one suggestion comment.
  - All comments are posted in a single review.

Examples:
  gh suggest --pr 123 /path/to/patch.diff
  git diff | gh suggest --pr 123 -
  git diff | gh suggest --pr 123 -R owner/repo
`)
	}
	flag.Parse()

	if prNumber <= 0 {
		fatalf("missing required --pr PR_NUM")
	}

	args := flag.Args()
	if len(args) != 1 {
		fatalf("missing required patch file path or '-' for stdin")
	}
	patchPath := args[0]

	repo, err := resolveRepo(repoArg)
	if err != nil {
		fatalf("resolve repo: %v", err)
	}

	patchBytes, err := readPatchInput(patchPath)
	if err != nil {
		fatalf("read patch: %v", err)
	}

	hunks, err := parsePatchHunks(string(patchBytes))
	if err != nil {
		fatalf("parse patch: %v", err)
	}

	if len(hunks) == 0 {
		fatalf("patch has no hunks")
	}

	client, err := api.NewRESTClient(api.ClientOptions{Host: repo.Host})
	if err != nil {
		fatalf("api client: %v", err)
	}

	prInfo, err := fetchPullRequest(client, repo, prNumber)
	if err != nil {
		fatalf("fetch PR: %v", err)
	}

	comments, err := buildReviewComments(hunks)
	if err != nil {
		fatalf("build comments: %v", err)
	}
	if dryRun {
		fmt.Print(formatCommentsMarkdown(hunks))
		return
	}

	if err := createReviewWithComments(client, repo, prNumber, prInfo.HeadSHA, comments); err != nil {
		fatalf("create review: %v", err)
	}

	fmt.Printf("created review suggestion on %s#%d\n", repoFullName(repo), prNumber)
}

type patchHunk struct {
	Path     string
	NewStart int
	NewCount int
	NewLines []string
}

func (h patchHunk) String() string {
	return fmt.Sprintf("path=%s\nnew_start=%d\nnew_count=%d\nnew_lines=\n```\n%s\n```", h.Path, h.NewStart, h.NewCount, strings.Join(h.NewLines, "\n"))
}

func resolveRepo(repoArg string) (repository.Repository, error) {
	if repoArg != "" {
		return repository.Parse(repoArg)
	}
	return repository.Current()
}

func parsePatchHunks(input string) ([]patchHunk, error) {
	files, _, err := gitdiff.Parse(strings.NewReader(input))
	if err != nil {
		return nil, err
	}

	hunks := []patchHunk{}
	for _, file := range files {
		if file.IsBinary || file.IsDelete || file.NewName == "" || file.NewName == "/dev/null" {
			continue
		}

		path := strings.TrimPrefix(file.NewName, "b/")
		for _, frag := range file.TextFragments {
			newLines := make([]string, 0, int(frag.NewLines))
			for _, line := range frag.Lines {
				if line.NoEOL() {
					continue
				}
				if line.New() {
					newLines = append(newLines, line.Line)
				}
			}

			hunks = append(hunks, patchHunk{
				Path:     path,
				NewStart: int(frag.NewPosition),
				NewCount: int(frag.NewLines),
				NewLines: newLines,
			})
		}
	}

	if len(hunks) == 0 {
		return nil, errors.New("no hunks found")
	}

	return hunks, nil
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

func createReviewWithComments(client *api.RESTClient, repo repository.Repository, prNumber int, headSHA string, comments []map[string]interface{}) error {
	if len(comments) == 0 {
		return errors.New("no review comments to create")
	}

	payload := map[string]interface{}{
		"commit_id": headSHA,
		"body":      "Suggestions from gh-suggest.",
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

func buildReviewComments(hunks []patchHunk) ([]map[string]interface{}, error) {
	comments := make([]map[string]interface{}, 0, len(hunks))
	for _, hunk := range hunks {
		if len(hunk.NewLines) == 0 {
			return nil, fmt.Errorf("hunk for %s has no added/modified lines; cannot create a suggestion", hunk.Path)
		}

		endLine := hunk.NewStart + len(hunk.NewLines) - 1
		body := buildSuggestionBody(hunk.NewLines)
		comment := map[string]interface{}{
			"path": hunk.Path,
			"side": "RIGHT",
			"line": endLine,
			"body": body,
		}
		if len(hunk.NewLines) > 1 {
			comment["start_side"] = "RIGHT"
			comment["start_line"] = hunk.NewStart
		}
		comments = append(comments, comment)
	}
	return comments, nil
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

func formatCommentsMarkdown(hunks []patchHunk) string {
	var b strings.Builder
	for i, hunk := range hunks {
		if len(hunk.NewLines) == 0 {
			continue
		}
		start := hunk.NewStart
		end := hunk.NewStart + len(hunk.NewLines) - 1
		if i > 0 {
			b.WriteString("\n")
		}
		if start == end {
			fmt.Fprintf(&b, "### `%s:%d`\n\n", hunk.Path, start)
		} else {
			fmt.Fprintf(&b, "### `%s:%d-%d`\n\n", hunk.Path, start, end)
		}
		b.WriteString(buildSuggestionBody(hunk.NewLines))
		b.WriteString("\n")
	}
	return b.String()
}

func readPatchInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	if path == "" {
		return nil, errors.New("missing required patch file path or '-' for stdin")
	}
	return os.ReadFile(path)
}
