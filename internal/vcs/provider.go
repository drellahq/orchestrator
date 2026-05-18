package vcs

import "context"

// Provider defines the operations that a VCS platform (GitHub, GitLab, etc.)
// must support.
type Provider interface {
	// Identity
	AuthenticatedUser(ctx context.Context) (string, error)

	// PR lifecycle
	EnsureFork(ctx context.Context, upstream string) (string, error)
	PushBranch(ctx context.Context, repoDir, forkFullName, branch, sourceRef string) error
	CreatePR(ctx context.Context, upstream, forkOwner, branch, base, title, body string) (string, error)
	AddCoAuthorTrailers(ctx context.Context, repoDir, upstream, base, sourceRef, trailer string) error
	CommentOnPR(ctx context.Context, prURL, body string) error
	UpdatePRTitle(ctx context.Context, prURL, title string) error
	PostReview(ctx context.Context, repo string, pr int, event, body string) error

	// Issues
	CommentOnIssue(ctx context.Context, repo string, issue int, body string) error
	ReactToIssue(ctx context.Context, repo string, issueNumber int, reaction string) error

	// PR state + comments
	IsPROpen(ctx context.Context, repo string, prNumber int) (bool, error)
	FetchAllComments(ctx context.Context, repo string, prNumber int) ([]Comment, error)
	ReactToComment(ctx context.Context, repo string, commentID int64, commentType CommentType, reaction string) error

	// Repository content
	ListRepoFiles(ctx context.Context, repo, branch, dir string) ([]string, error)
	GetFileContent(ctx context.Context, repo, branch, path string) (string, error)
	ListIssues(ctx context.Context, repo string) ([]Issue, error)

	// Repository cloning
	CloneRepo(ctx context.Context, repo, dir string) error

	// URL parsing
	PRNumberFromURL(url string) (int, error)

	// Remote URL construction
	RepoURL(repo string) string
}
