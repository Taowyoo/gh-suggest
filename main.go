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
  - To minimize suggestion size in GitHub PRs, generate the patch with "git diff -U0".

Examples:
  gh suggest --pr 123 /path/to/patch.diff
  git diff -U0 | gh suggest --pr 123 -
  git diff -U0 | gh suggest --pr 123 -R owner/repo -
`)
	}
	flag.Parse()

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

	comments, err := buildReviewComments(hunks)
	if err != nil {
		fatalf("build comments: %v", err)
	}
	if dryRun {
		if prNumber <= 0 {
			fmt.Print(formatCommentsMarkdown(hunks))
			return
		}
		fmt.Printf("would create review suggestion on %s\n\n", prURL(repo, prNumber))
		fmt.Print(formatCommentsMarkdown(hunks))
		return
	}

	if prNumber <= 0 {
		fatalf("missing required --pr PR_NUM")
	}

	client, err := api.NewRESTClient(api.ClientOptions{Host: repo.Host})
	if err != nil {
		fatalf("api client: %v", err)
	}

	prInfo, err := fetchPullRequest(client, repo, prNumber)
	if err != nil {
		fatalf("fetch PR: %v", err)
	}

	if err := createReviewWithComments(client, repo, prNumber, prInfo.HeadSHA, comments); err != nil {
		fatalf("create review: %v", err)
	}

	fmt.Printf("created review suggestion on %s#%d\n", repoFullName(repo), prNumber)
}

type patchHunk struct {
	Path     string
	Side     string
	NewStart int
	NewCount int
	OldStart int
	OldCount int
	NewLines []string
}

func (h patchHunk) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n", h.Path, h.Path)
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldCount, h.NewStart, h.NewCount)
	if h.Side == "RIGHT" {
		for _, line := range h.NewLines {
			b.WriteString("+")
			b.WriteString(line)
		}
	} else {
		// Deletion-only hunks have no new lines captured in NewLines.
	}
	return b.String()
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
			if frag.NewLines == 0 && frag.OldLines > 0 {
				hunks = append(hunks, patchHunk{
					Path:     path,
					Side:     "LEFT",
					OldStart: int(frag.OldPosition),
					OldCount: int(frag.OldLines),
					NewStart: int(frag.NewPosition),
					NewCount: int(frag.NewLines),
					NewLines: nil,
				})
				continue
			}
			if len(newLines) == 0 {
				continue
			}

			hunks = append(hunks, patchHunk{
				Path:     path,
				Side:     "RIGHT",
				NewStart: int(frag.NewPosition),
				NewCount: int(frag.NewLines),
				OldStart: int(frag.OldPosition),
				OldCount: int(frag.OldLines),
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
	if len(lines) == 0 {
		return "```suggestion\n```"
	}
	return "```suggestion\n" + strings.Join(lines, "") + "```"
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
		body := buildSuggestionBody(hunk.NewLines)
		var comment map[string]interface{}
		switch hunk.Side {
		case "LEFT":
			if hunk.OldCount == 0 {
				return nil, fmt.Errorf("hunk for %s has no deleted lines; cannot create a suggestion", hunk.Path)
			}
			endLine := hunk.OldStart + hunk.OldCount - 1
			comment = map[string]interface{}{
				"path": hunk.Path,
				"side": "LEFT",
				"line": endLine,
				"body": body,
			}
			if hunk.OldCount > 1 {
				comment["start_side"] = "LEFT"
				comment["start_line"] = hunk.OldStart
			}
		default:
			if len(hunk.NewLines) == 0 {
				return nil, fmt.Errorf("hunk for %s has no added/modified lines; cannot create a suggestion", hunk.Path)
			}
			if hunk.NewCount == 0 {
				hunk.NewCount = len(hunk.NewLines)
			}
			endLine := hunk.NewStart + hunk.NewCount - 1
			comment = map[string]interface{}{
				"path": hunk.Path,
				"side": "RIGHT",
				"line": endLine,
				"body": body,
			}
			if hunk.NewCount > 1 {
				comment["start_side"] = "RIGHT"
				comment["start_line"] = hunk.NewStart
			}
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

func prURL(repo repository.Repository, prNumber int) string {
	host := repo.Host
	if host == "" {
		host = "github.com"
	}
	return fmt.Sprintf("https://%s/%s/%s/pull/%d", host, repo.Owner, repo.Name, prNumber)
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func formatCommentsMarkdown(hunks []patchHunk) string {
	var b strings.Builder
	for i, hunk := range hunks {
		if hunk.Side == "LEFT" && hunk.OldCount == 0 {
			continue
		}
		if hunk.Side != "LEFT" && len(hunk.NewLines) == 0 {
			continue
		}
		start := hunk.NewStart
		end := hunk.NewStart + len(hunk.NewLines) - 1
		if hunk.Side == "LEFT" {
			start = hunk.OldStart
			end = hunk.OldStart + hunk.OldCount - 1
		}
		if i > 0 {
			b.WriteString("\n")
		}
		if start == end {
			fmt.Fprintf(&b, "### `%s:%d`\n", hunk.Path, start)
		} else {
			fmt.Fprintf(&b, "### `%s:%d-%d`\n", hunk.Path, start, end)
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
