# Orchestrator

A CLI tool that spawns sandboxed Claude instances using [gjoll](https://github.com/ondrejbudai/gjoll) (libvirt backend), exposes an MCP server for privileged actions (pulling code), and manages task lifecycle including conversation archival and code retrieval.

The orchestrator bridges untrusted sandbox execution with trusted host operations: Claude runs inside a credential-free VM, but can request the host to pull committed code via MCP.

## Prerequisites

- **Go** (1.24+)
- **gjoll**: `go install github.com/ondrejbudai/gjoll/cmd/gjoll@latest`
- **libvirt**: `virsh version` must work
- **OpenTofu**: `tofu version` must work
- **GCP Application Default Credentials**: for Vertex AI proxying (`gcloud auth application-default login`)
- **gh** (optional): GitHub CLI, authenticated (`gh auth login`) — required for `open_pr`

## Installation

```bash
go install github.com/drellabot/orchestrator/cmd/orchestrator@latest
```

Or build from source:

```bash
git clone https://github.com/drellabot/orchestrator.git
cd orchestrator
go build -o orchestrator ./cmd/orchestrator
```

## Setup

1. Edit `configs/sandbox.tf` and replace `YOUR_PROJECT_ID_HERE` with your GCP project ID for Vertex AI.

2. Copy the example config and adjust if needed:

   ```bash
   cp orchestrator.yaml.example orchestrator.yaml
   ```

3. Ensure libvirt's default network is active:

   ```bash
   sudo virsh net-start default
   ```

4. (Optional) Install the systemd user service for daemon mode:

   ```bash
   cp dist/orchestrator.service ~/.config/systemd/user/
   systemctl --user daemon-reload
   systemctl --user enable --now orchestrator.service
   ```

   Check status and view logs:

   ```bash
   systemctl --user status orchestrator
   journalctl --user -u orchestrator -f
   ```

   To enable the service to run without an active login session:

   ```bash
   loginctl enable-linger $USER
   ```

## Configuration

The `orchestrator.yaml` file supports:

| Field           | Default              | Description                                |
|-----------------|----------------------|--------------------------------------------|
| `slack_webhook` | (empty)              | Slack webhook URL for task notifications   |
| `output_dir`    | `./tasks`            | Directory for task output                  |
| `gjoll_env`     | `./configs/sandbox.tf` | Path to gjoll .tf environment file       |
| `allowed_repos` | `[]` (deny all)      | Repos allowed for `open_pr`/`update_pr`/`comment_on_pr` (glob patterns)|
| `profiles_repo` | (empty)              | GitHub repo containing profile directories (e.g. `myorg/profiles`) |
| `profiles_dir`  | (empty)              | Local directory override for profiles (takes precedence over `profiles_repo`) |
| `daemon.poll_interval` | `60s`         | How often the daemon polls for new PR comments and tasks |
| `daemon.allowed_commenters` | `[]`    | GitHub usernames allowed to trigger `task continue` via PR comments and to create tasks via `tasks_repo` issues |
| `daemon.tasks_repo` | (empty)          | GitHub repo to monitor for task specs and issues (e.g. `myorg/tasks`) |

## Usage

```bash
orchestrator task new <task-name> <task-description...>
```

Example:

```bash
orchestrator task new fix-bug "Fix the nil pointer dereference in handler.go when the request body is empty"
```

Use `-v` for verbose output including debug logging and model reasoning:

```bash
orchestrator task new -v fix-bug "Fix the nil pointer dereference in handler.go"
```

Use `--profile` to apply a task-type-specific profile to the sandbox:

```bash
orchestrator task new --profile code-review review-pr-42 "Review PR #42 in org/repo"
```

Profiles provide custom CLAUDE.md instructions, setup scripts, MCP servers, and
Claude Code settings. They are loaded from `profiles_dir` (if set) or cloned from
`profiles_repo`. See [Profiles](#profiles) for details.

Use `--author` to add a `Co-authored-by` trailer to every PR commit:

```bash
orchestrator task new --author "Jane Doe <jane@example.com>" fix-bug "Fix the nil pointer dereference"
```

When set, the orchestrator fetches the upstream base branch before pushing and
rewrites new commits to include the trailer. Commits that already carry the
trailer are left unchanged. The flag works with both `open_pr` and `update_pr`
MCP tools. The author is persisted in the task state, so `task continue`
automatically uses the same author without needing to specify it again.

To continue a stopped task with a new prompt (resumes the VM and Claude conversation):

```bash
orchestrator task continue <task-name> <new-prompt...>
```

Example:

```bash
orchestrator task continue fix-bug "The tests are failing, please also update the test fixtures"
```

During execution, a human-readable transcript streams to stdout showing tool
calls, their results, and Claude's text output.

### Viewing transcripts

```bash
# View a completed task's transcript
orchestrator log <task-name>

# Include model reasoning (thinking blocks)
orchestrator log -v <task-name>

# Follow a live task in another terminal
orchestrator log -f <task-name>
```

### What happens

1. Task directory is created under `output_dir`
2. MCP server starts on `127.0.0.1:19090`
3. Sandbox VM is provisioned via gjoll
4. Git author, system prompt, and MCP client are configured in the VM
5. Claude runs in `$HOME` with `-p --output-format stream-json` (with proxy tunnels for Vertex AI and MCP)
6. Stream-json output is piped through `tee` to `~/transcript.jsonl` in the VM
7. On completion, the transcript and conversations are archived and the VM is stopped

## Task Output Structure

```
<output_dir>/<task-name>/
  repo/              # Pulled code (git repo with gjoll-<task-name> branch)
  conversations/     # Claude conversation archive (~/.claude/ from VM)
  transcript.jsonl   # Stream-json transcript of the Claude session
  state.json         # Task metadata and state (name, description, opened PRs)
```

## Architecture

```
Host                              Sandbox VM
+-----------+                     +------------------+
|orchestrator|--gjoll ssh/proxy-->| Claude Code      |
|           |                     |   (no credentials)|
| MCP Server|<--reverse tunnel----|   calls open_pr   |
| (port     |                     |                  |
|  19090)   |                     +------------------+
|           |
| Vertex    |--reverse tunnel---->  http://localhost:18080
| Proxy     |                       (GCP auth injected)
| (port     |
|  18080)   |
+-----------+
```

## Dashboard

A lightweight web UI for browsing tasks and reading transcripts. The dashboard is a static HTML/CSS/JS app (no build step) served alongside the task output directory by [Caddy](https://caddyserver.com/).

### Running

Copy and adjust the example Caddyfile, then start Caddy:

```bash
cp Caddyfile.example Caddyfile
caddy run
```

The dashboard is available at `http://localhost:2080`. Caddy serves the `dashboard/` directory for the UI and the `tasks/` directory (your `output_dir`) as a file-based API with directory browsing enabled.

### Features

- **Task list** — auto-refreshes every 30 seconds, shows name, description, author, and linked PRs
- **Transcript viewer** — renders Claude session transcripts with syntax-highlighted tool calls, collapsible thinking blocks, and sub-agent progress
- **Live tailing** — polls the transcript every 5 seconds via HTTP Range requests, so you can watch a running task in the browser
- **Keyboard shortcuts** — `Escape` to return to the task list, `r` to refresh

### How it works

The dashboard has no backend of its own. Caddy's file server acts as the API:

| Endpoint | Source | Purpose |
|---|---|---|
| `GET /tasks/` | `output_dir` directory listing | Discover tasks |
| `GET /tasks/<name>/state.json` | Task state file | Task metadata |
| `GET /tasks/<name>/transcript.jsonl` | Transcript file | Session transcript |

Make sure the Caddyfile `root` for `/tasks/*` points at the same directory as `output_dir` in `orchestrator.yaml` (default `./tasks`).

## Profiles

Profiles let each task type carry its own Claude configuration — `CLAUDE.md` instructions, setup scripts, MCP servers, and settings — in a separate profiles repository.

### Profile directory structure

Each profile is a directory containing some combination of:

```
profiles-repo/
  default/
    CLAUDE.md           # Workflow instructions (required)
  code-review/
    CLAUDE.md           # Review instructions (required)
    setup.sh            # Workspace setup script (optional, runs on host)
    mcp.yaml            # Additional MCP servers (optional)
    settings.json       # Claude Code settings (optional)
```

Only `CLAUDE.md` is required. All other files are optional and processed only if present.

### How profiles are applied

When `--profile <name>` is specified on `task new`:

1. The profile is loaded from `profiles_dir` (local, for development) or cloned from `profiles_repo`
2. A base environment prompt + the profile's `CLAUDE.md` are written to `~/.claude/CLAUDE.md` in the sandbox
3. `settings.json` is copied to `~/.claude/settings.json` (if present)
4. MCP servers from `mcp.yaml` are registered via `claude mcp add` (if present)
5. `setup.sh` runs **on the host** with helper commands on PATH (if present)

When no `--profile` is specified, existing behavior is preserved (system prompt via `--append-system-prompt-file`).

### setup.sh environment

The orchestrator provides these environment variables to `setup.sh`:

| Variable | Description |
|----------|-------------|
| `SANDBOX` | Sandbox name (for gjoll operations) |
| `TASK_DIR` | Task output directory on host |
| `PROFILE_*` | Front matter key-value pairs (keys uppercased, hyphens → underscores) |

Two helper commands are available on PATH:

- **`sandbox-cp <local-path> <sandbox-path>`** — copy files to the sandbox
- **`sandbox-ssh <command>`** — run a command inside the sandbox

### mcp.yaml format

```yaml
servers:
  - name: my-tool
    transport: stdio        # "stdio" or "http"
    command: npx             # stdio: command to run
    args: ["-y", "pkg@latest"] # stdio: command arguments
  - name: web-tool
    transport: http
    url: http://localhost:8080/mcp
    scope: user              # optional
```

### Issue front matter

When processing issues, YAML front matter can specify a profile and pass variables to `setup.sh`:

```markdown
---
profile: code-review
repo: org/repo
pr: 42
---

Review this pull request.
```

The `profile` key selects the profile. Other keys become `PROFILE_*` environment variables.

## Running Tests

### Unit tests

```bash
go test ./...
```

### Integration test (requires libvirt)

```bash
go test -tags integration -v -timeout 30m
```

See [HACKING.md](HACKING.md) for detailed setup instructions.
