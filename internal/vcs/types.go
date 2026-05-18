package vcs

// CommentType distinguishes top-level from review comments.
type CommentType string

const (
	IssueComment  CommentType = "issue"
	ReviewComment CommentType = "review"
)

// Comment is a unified representation of PR/MR comments.
type Comment struct {
	ID        int64       `json:"id"`
	Body      string      `json:"body"`
	User      CommentUser `json:"user"`
	CreatedAt string      `json:"created_at"`
	Type      CommentType `json:"-"`
	HTMLURL   string      `json:"html_url,omitempty"`

	// Review comment fields (only set for review comments)
	Path     string `json:"path,omitempty"`
	DiffHunk string `json:"diff_hunk,omitempty"`
}

// CommentUser holds the login of the comment author.
type CommentUser struct {
	Login string `json:"login"`
}

// Issue represents a repository issue.
type Issue struct {
	Number int         `json:"number"`
	Title  string      `json:"title"`
	Body   string      `json:"body"`
	User   CommentUser `json:"user"`
}
