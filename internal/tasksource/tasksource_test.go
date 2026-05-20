package tasksource

import "testing"

func TestIssueNumberFromTaskName(t *testing.T) {
	tests := []struct {
		tasksRepo string
		taskName  string
		want      int
		ok        bool
	}{
		{"org/tasks", "tasks-42-add_dark_mode", 42, true},
		{"org/my-repo", "my-repo-1-fix_the_login_bug", 1, true},
		{"simple", "simple-10-hello_world", 10, true},
		{"org/tasks", "tasks-7-feature_add_sso_support", 7, true},
		{"org/tasks", "tasks-3-extra_spaces", 3, true},
		{"org/tasks", "other-42-add_dark_mode", 0, false},
		{"org/tasks", "tasks-not-a-number-title", 0, false},
		{"org/tasks", "tasks-42", 0, false},
		{"org/tasks", "", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.taskName, func(t *testing.T) {
			got, ok := IssueNumberFromTaskName(tt.tasksRepo, tt.taskName)
			if ok != tt.ok {
				t.Errorf("ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRepoBasename(t *testing.T) {
	if got := RepoBasename("org/tasks"); got != "tasks" {
		t.Errorf("got %q, want tasks", got)
	}
	if got := RepoBasename("simple"); got != "simple" {
		t.Errorf("got %q, want simple", got)
	}
}
