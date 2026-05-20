package tasksource

import (
	"strconv"
	"strings"
)

// RepoBasename returns the repository name portion of owner/repo.
func RepoBasename(repo string) string {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return repo
}

// IssueNumberFromTaskName extracts the issue number from a task name created
// by the daemon from a tasks-repo issue. Format: REPO_NAME-ISSUE_NUMBER-snake_title.
func IssueNumberFromTaskName(tasksRepo, taskName string) (int, bool) {
	prefix := RepoBasename(tasksRepo) + "-"
	rest, ok := strings.CutPrefix(taskName, prefix)
	if !ok {
		return 0, false
	}
	numStr, _, ok := strings.Cut(rest, "-")
	if !ok || numStr == "" {
		return 0, false
	}
	n, err := strconv.Atoi(numStr)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
