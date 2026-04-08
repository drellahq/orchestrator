package prompts

import _ "embed"

//go:embed on_init.md
var OnInit string

//go:embed on_pr_comment.md
var OnPRComment string

//go:embed base.md
var Base string
