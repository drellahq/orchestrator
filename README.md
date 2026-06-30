# Orchestrator

A CLI tool that spawns sandboxed coding agent instances using [gjoll](https://github.com/ondrejbudai/gjoll) (VM) or podman (container) backends, exposes an MCP server for privileged actions (pulling code), and manages task lifecycle including conversation archival and code retrieval. Supports [Claude Code](https://claude.ai) and [OpenCode](https://opencode.ai/) as agent backends.

The orchestrator bridges untrusted sandbox execution with trusted host operations: the agent runs inside a credential-free VM, but can request the host to pull committed code via MCP.

Run `make check` to verify prerequisites before your first task.

## Prerequisites

For local dev with Podman (no libvirt or GCP), see [Local Development](#local-development) instead.

- **Go** (1.24+)
- **gjoll**: `go install github.com/ondrejbudai/gjoll/cmd/gjoll@latest`
- **libvirt**: `virsh version` must work
- **OpenTofu**: `tofu version` must work
- **GCP Application Default Credentials**: for Vertex AI proxying (`gcloud auth application-default login`)
- **gh** (optional): GitHub CLI, authenticated (`gh auth login`) — required for `open_pr`

## Installation

```bash
go install github.com/drellahq/orchestrator/cmd/orchestrator@latest
```

Or build from source:

```bash
git clone https://github.com/drellahq/orchestrator.git
cd orchestrator
make build
```

## Setup

For the lighter Podman path, skip to [Local Development → Option 1](#option-1-podman-backend-containers). For libvirt with Anthropic API instead of Vertex AI, see [Option 2](#option-2-gjoll-backend-with-direct-anthropic-api).

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

| Field                       | Default                   | Description                                |
|-----------------------------|---------------------------|--------------------------------------------|
| `slack_webhook`             | (empty)                   | Slack webhook URL for task notifications   |
| `output_dir`                | `./tasks`                 | Directory for task output                  |
| `sandbox_backend`           | `gjoll`                   | Sandbox backend: `gjoll` (VMs) or `podman` (containers) |
| `gjoll_env`                 | `./configs/sandbox.tf`    | Path to gjoll .tf environment file (gjoll backend) |
| `podman_image`              | `fedora:43`               | Container image for sandboxes (podman backend) |
| `anthropic_key_file`        | (empty)                   | Path to Anthropic API key when using cloud Anthropic (not needed with default LM Studio) |
| `agent_backend`             | `opencode`                | Coding agent backend: `claude-code` or `opencode` |
| `llm_base_url`              | `http://127.0.0.1:1234/v1` | Local LLM API base URL (LM Studio); set empty to use cloud Anthropic/Vertex |
| `allowed_repos`             | `[]` (deny all)           | Repos allowed for `open_pr`/`update_pr`/`comment_on_pr` (glob patterns)|
| `profiles_repo`             | (empty)                   | GitHub repo containing profile directories (e.g. `myorg/profiles`) |
| `profiles_dir`              | (empty)                   | Local directory override for profiles (takes precedence over `profiles_repo`; e.g. `../profiles` in a multi-repo checkout) |
| `daemon.poll_interval`      | `60s`                     | How often the daemon polls for new PR comments and tasks |
| `daemon.allowed_commenters` | `[]`                      | GitHub usernames allowed to trigger `task continue` via PR comments and to create tasks via `tasks_repo` issues |
| `daemon.tasks_repo`         | (empty)                   | GitHub repo to monitor for task specs and issues (e.g. `myorg/tasks`) |

## Local Development

The orchestrator supports two sandbox backends for local development:

### Option 1: Podman Backend (Containers)

Faster and lighter for local development. Sandboxes run as podman containers instead of VMs.

**Prerequisites:**
- **Podman**: `podman version` must work
- **[LM Studio](https://lmstudio.ai/)**: local server running with a model loaded (default API: `http://127.0.0.1:1234/v1`)

No Anthropic API key is required with the default configuration — agents talk to LM Studio via `llm_base_url`.

**Setup:**

1. Start LM Studio, load a model, and enable the local server.

2. Copy and adjust config (set `sandbox_backend: podman` for the fastest local path):
   ```bash
   cp orchestrator.yaml.example orchestrator.yaml
   ```

   Minimal `orchestrator.yaml`:
   ```yaml
   sandbox_backend: podman
   output_dir: "./tasks"
   allowed_repos:
     - your-username/*
   ```

3. Verify prerequisites:

   ```bash
   make check
   ```

4. Authenticate with GitHub CLI (for PR operations):
   ```bash
   gh auth login
   ```

5. Run a task:
   ```bash
   ./orchestrator task new hello-test "Create a hello.txt file"
   ```

**How it works:**
- Podman containers are created on-demand for each task
- OpenCode is installed in the container (default `agent_backend`)
- The agent uses `ANTHROPIC_BASE_URL` pointing at LM Studio on the host (`--network host`)
- Containers run as non-root user `claude`
- Faster startup than VMs (seconds vs minutes)

To use cloud Anthropic instead, sign up at [console.anthropic.com](https://console.anthropic.com), save your API key to `~/.anthropic/api_key`, and configure:

```yaml
llm_base_url: ""
anthropic_key_file: "~/.anthropic/api_key"
```

### Option 2: Gjoll Backend with Local LLM

Use gjoll libvirt VMs with a local LLM (Ollama or LM Studio) via gjoll's HTTP passthrough proxy.

**Setup:**

1. Install gjoll from a build that includes HTTP proxy support (see [gjoll](https://github.com/ondrejbudai/gjoll)).

2. Configure `orchestrator.yaml`:
   ```yaml
   sandbox_backend: "gjoll"
   gjoll_env: "../gjoll/examples/fedora-libvirt.tf"
   llm_base_url: "http://127.0.0.1:11434/v1"
   llm_model: "your-model-id"
   output_dir: "./tasks"
   allowed_repos:
     - your-username/*
   ```

The orchestrator sets `TF_VAR_llm_host_port` so the VM can reach your host LLM through gjoll's reverse proxy.

### Option 3: Gjoll Backend with Direct Anthropic API

Use gjoll VMs (same as cloud deployment) but with direct Anthropic API instead of Vertex AI.

**Prerequisites:**
- Same as main prerequisites above, plus:
- **Anthropic API Key**: Sign up at [console.anthropic.com](https://console.anthropic.com) and save your API key to `~/.anthropic/api_key` on the orchestrator host

**Setup:**

1. Copy the Anthropic API example config:
   ```bash
   cp configs/sandbox-anthropic-api.tf.example configs/sandbox-anthropic.tf
   ```

2. Configure `orchestrator.yaml`:
   ```yaml
   sandbox_backend: "gjoll"
   gjoll_env: "./configs/sandbox-anthropic.tf"
   llm_base_url: ""
   output_dir: "./tasks"
   allowed_repos:
     - your-username/*
   ```

3. Ensure the API key is readable:
   ```bash
   chmod 600 ~/.anthropic/api_key
   ```

**Differences from Vertex AI setup:**
- Uses `auth: "api-key"` instead of `auth: "gcp"` in the proxy configuration
- Gjoll proxy reads `~/.anthropic/api_key` and injects `x-api-key` header
- No GCP credentials needed

### Comparison

| Feature | Podman Backend | Gjoll Backend |
|---------|----------------|---------------|
| Startup time | Seconds | Minutes |
| Resource usage | Low | Higher (full VM) |
| Isolation | Container | Full VM |
| Nested virtualization | Not needed | May be needed |
| Best for | Local development, iteration | Testing VM workflows, cloud-like setup |

### Agent Backend

The orchestrator supports multiple coding agent backends. The default is
[OpenCode](https://opencode.ai/) with a local [LM Studio](https://lmstudio.ai/)
LLM. Switch to Claude Code by setting `agent_backend` in `orchestrator.yaml`:

```yaml
agent_backend: "claude-code"   # default: "opencode"
```

The backend controls how the agent is installed, invoked, and how transcripts
are parsed. Both backends produce the same human-readable output for
`orchestrator log` and the dashboard.

| Feature | `claude-code` | `opencode` |
|---------|---------------|------------|
| Install | `curl -fsSL https://claude.ai/install.sh \| bash` | `curl -fsSL https://opencode.ai/install \| bash` |
| Runtime | Node.js | Standalone binary (Go) |
| MCP registration | `claude mcp add` CLI | `opencode.json` config file |
| System prompt | `--append-system-prompt-file` | Merged into CLAUDE.md |
| Transcript format | `stream-json` | `--format json` |

When using the podman sandbox backend, the agent is installed automatically in
the container. When using gjoll, the agent must be installed by the `.tf` file's
`init_script` output — see the [gjoll examples](https://github.com/drellabot/gjoll/tree/main/examples)
for both Claude Code and OpenCode variants.

**Per-task override.** You can override the backend for a single task using the
`--agent-backend` flag:

```bash
orchestrator task new --agent-backend opencode my-task "Fix the bug"
```

When creating tasks from GitHub issues, add `agent: opencode` to the task header
(same syntax as `profile:`):

````markdown
```
agent: opencode
profile: code-review
repo: org/repo
```

Review this pull request.
````

The per-task override takes precedence over the `agent_backend` config field.

### Issue attachments

When a task is created from a tasks-repo GitHub issue (`--source-repo` / `--source-issue`, set automatically by the daemon), the orchestrator scans the **task description** for `https://github.com/user-attachments/...` links (the daemon passes the full issue body, so no extra API call is needed). If the description has no such links but `--source-issue` is set, the issue body is re-fetched via `gh api` before scanning. Matching files are downloaded on the host using `gh` credentials and copied into the sandbox at `~/attachments/`. The initial Claude prompt includes a manifest listing the local filenames. Requires an authenticated `gh` on the host.

**See also:** [Dashboard](#dashboard) for browsing tasks in the browser; [Setup](#setup) step 4 for daemon mode via systemd; `make check` to verify prerequisites.

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

### List and remove tasks

List tasks stored under `output_dir`:

```bash
orchestrator task list
orchestrator task list --json
```

Destroy a task's sandbox (podman container or gjoll VM) and remove its local
`repo/` directory. The task directory itself (`state.json`, transcript,
conversations) is kept:

```bash
orchestrator task rm <task-name>
orchestrator task rm hello-test-8 -y
orchestrator task rm hello-test-8 --dry-run
```

Use `--force` to destroy sandboxes for tasks that are `in_progress` or still
have open PRs. The daemon performs similar cleanup automatically when PRs close;
`task rm` is for manual/local cleanup without running the daemon.

During execution, a human-readable transcript streams to stdout showing tool
calls, their results, and the agent's text output. For OpenCode, tool output is
included inline after each `[tool]` line; use `-v` to also show `thinking`
blocks when the agent emits them (uncommon with some local models).

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
| `GET /version.json` | Written by daemon at startup | Orchestrator & OS version info |

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

### Task header

When processing issues, a task header can specify a profile and pass variables to `setup.sh`.
Use backtick-fenced blocks so the header renders as a code block in Markdown:

````markdown
```
profile: code-review
repo: org/repo
pr: 42
```

Review this pull request.
````

The `profile` key selects the profile. Other keys become `PROFILE_*` environment variables.

`---` delimiters are also accepted for backward compatibility.

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
