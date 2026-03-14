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

## Configuration

The `orchestrator.yaml` file supports:

| Field             | Default              | Description                                              |
|-------------------|----------------------|----------------------------------------------------------|
| `slack_bot_token` | (empty)              | Slack bot token (`xoxb-...`) with `chat:write` scope     |
| `slack_channel`   | (empty)              | Slack channel ID to post to (e.g. `C0123456789`)         |
| `output_dir`      | `./tasks`            | Directory for task output                                |
| `gjoll_env`       | `./configs/sandbox.tf` | Path to gjoll .tf environment file                     |
| `allowed_repos`   | `[]` (deny all)      | Repos allowed for `open_pr` (glob patterns)              |

When both `slack_bot_token` and `slack_channel` are set, the orchestrator posts an initial task summary to the channel and threads all subsequent updates as replies.

## Usage

```bash
orchestrator run <task-name> <task-description...>
```

Example:

```bash
orchestrator run fix-bug "Fix the nil pointer dereference in handler.go when the request body is empty"
```

Use `-v` for verbose output including debug logging and model reasoning:

```bash
orchestrator run -v fix-bug "Fix the nil pointer dereference in handler.go"
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
4. Git, CLAUDE.md, and MCP client are configured in the VM
5. Claude runs with `-p --output-format stream-json` (with proxy tunnels for Vertex AI and MCP)
6. Stream-json output is piped through `tee` to `~/transcript.jsonl` in the VM
7. On completion, the transcript and conversations are archived and the VM is stopped

## Task Output Structure

```
<output_dir>/<task-name>/
  repo/              # Pulled code (git repo with gjoll/<task-name> branch)
  conversations/     # Claude conversation archive (~/.claude/ from VM)
  transcript.jsonl   # Stream-json transcript of the Claude session
  metadata.json      # Task name, description, timestamps
```

## Architecture

```
Host                              Sandbox VM
+-----------+                     +------------------+
|orchestrator|--gjoll ssh/proxy-->| Claude Code      |
|           |                     |   (no credentials)|
| MCP Server|<--reverse tunnel----|   calls pull_code |
| (port     |                     |                  |
|  19090)   |                     +------------------+
|           |
| Vertex    |--reverse tunnel---->  http://localhost:18080
| Proxy     |                       (GCP auth injected)
| (port     |
|  18080)   |
+-----------+
```

## Running Tests

### Unit tests

```bash
go test ./...
```

### Integration test (requires libvirt)

```bash
go test -tags integration -v -timeout 10m
```
