package github

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/drellabot/orchestrator/internal/vcs"
)

// Type aliases for backward compatibility.
// Consumers that reference github.Comment, github.CommentUser, etc.
// will continue to work without changes.
type Comment = vcs.Comment
type CommentUser = vcs.CommentUser
type CommentType = vcs.CommentType

const (
	IssueComment  = vcs.IssueComment
	ReviewComment = vcs.ReviewComment
)


// ListIssueComments fetches top-level conversation comments on a PR.
func (r *Runner) ListIssueComments(ctx context.Context, repo string, prNumber int) ([]vcs.Comment, error) {
	out, err := r.run(ctx, "", r.bin, "api", "--paginate",
		fmt.Sprintf("/repos/%s/issues/%d/comments", repo, prNumber))
	if err != nil {
		return nil, fmt.Errorf("listing issue comments: %w", err)
	}
	comments, err := parseComments(out)
	if err != nil {
		return nil, err
	}
	for i := range comments {
		comments[i].Type = vcs.IssueComment
	}
	return comments, nil
}

// ListReviewComments fetches line-level review comments on a PR.
func (r *Runner) ListReviewComments(ctx context.Context, repo string, prNumber int) ([]vcs.Comment, error) {
	out, err := r.run(ctx, "", r.bin, "api", "--paginate",
		fmt.Sprintf("/repos/%s/pulls/%d/comments", repo, prNumber))
	if err != nil {
		return nil, fmt.Errorf("listing review comments: %w", err)
	}
	comments, err := parseComments(out)
	if err != nil {
		return nil, err
	}
	for i := range comments {
		comments[i].Type = vcs.ReviewComment
	}
	return comments, nil
}

// IsPROpen checks whether a PR is still open.
func (r *Runner) IsPROpen(ctx context.Context, repo string, prNumber int) (bool, error) {
	out, err := r.run(ctx, "", r.bin, "api",
		fmt.Sprintf("/repos/%s/pulls/%d", repo, prNumber),
		"--jq", ".state")
	if err != nil {
		return false, fmt.Errorf("checking PR state: %w", err)
	}
	return strings.TrimSpace(out) == "open", nil
}

// FetchAllComments retrieves both issue and review comments, merges them,
// and returns the result sorted by ID.
func (r *Runner) FetchAllComments(ctx context.Context, repo string, prNumber int) ([]vcs.Comment, error) {
	issue, err := r.ListIssueComments(ctx, repo, prNumber)
	if err != nil {
		return nil, err
	}
	review, err := r.ListReviewComments(ctx, repo, prNumber)
	if err != nil {
		return nil, err
	}
	all := append(issue, review...)
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return all, nil
}

// parseComments handles gh api --paginate output, which may concatenate
// multiple JSON arrays (one per page).
func parseComments(out string) ([]vcs.Comment, error) {
	out = strings.TrimSpace(out)
	if out == "" || out == "[]" {
		return nil, nil
	}

	var all []vcs.Comment
	dec := json.NewDecoder(strings.NewReader(out))
	for dec.More() {
		var page []vcs.Comment
		if err := dec.Decode(&page); err != nil {
			return nil, fmt.Errorf("parsing comments JSON: %w", err)
		}
		all = append(all, page...)
	}
	return all, nil
}
