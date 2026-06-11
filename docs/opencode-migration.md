# Replacing Claude Code with OpenCode: Investigation

## Overview

This document investigates replacing Claude Code with [OpenCode](https://opencode.ai/)
as the coding agent in the orchestrator. OpenCode is an open-source AI coding agent
(169K+ GitHub stars) that supports 75+ model providers and has explicit Claude Code
compatibility features, including native CLAUDE.md reading.

## Proof-of-Concept Findings

### Installation

OpenCode installs via a single curl command, similar to Claude Code:

```bash
# Claude Code
curl -fsSL https://claude.ai/install.sh | bash

# OpenCode
curl -fsSL https://opencode.ai/install | bash
```

Binary lands in `~/.opencode/bin/opencode` (vs `~/.local/bin/claude`).

### Headless Execution

Both support headless execution with permission bypass:

```bash
# Claude Code
claude --dangerously-skip-permissions -p --output-format stream-json 'task description'

# OpenCode
opencode run --dangerously-skip-permissions --format json 'task description'
```

Key differences in flags:

| Feature | Claude Code | OpenCode |
|---------|-------------|----------|
| Headless mode | `-p` | `run` subcommand |
| Permission bypass | `--dangerously-skip-permissions` | `--dangerously-skip-permissions` (same) |
| JSON output | `--output-format stream-json` | `--format json` |
| Verbosity | `--verbose` | `--log-level DEBUG` |
| Effort level | `--effort max` | `--variant max` |
| System prompt file | `--append-system-prompt-file <path>` | No CLI equivalent (use config) |
| Continue session | `--continue` | `--continue` (same) |

### JSON Output Format (Critical Difference)

The transcript/streaming JSON formats are **completely different**:

**Claude Code `stream-json`:**
```json
{"type":"system","subtype":"init","model":"...","session_id":"..."}
{"type":"assistant","message":{"content":[{"type":"text","text":"..."}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"..."}]}}
{"type":"result","subtype":"done","duration_ms":1234,"total_cost_usd":0.05,"usage":{"input_tokens":100,"output_tokens":50}}
```

**OpenCode `--format json`:**
```json
{"type":"step_start","timestamp":123,"sessionID":"...","part":{"type":"step-start"}}
{"type":"tool_use","timestamp":123,"sessionID":"...","part":{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"..."},"output":"..."}}}
{"type":"text","timestamp":123,"sessionID":"...","part":{"type":"text","text":"..."}}
{"type":"step_finish","timestamp":123,"sessionID":"...","part":{"reason":"stop","tokens":{"total":100,"input":80,"output":20}}}
```

This affects: `internal/cmd/log.go` (transcript formatter), `internal/task/usage.go`
(usage parser), `dashboard/app.js` (live transcript viewer).

### CLAUDE.md Compatibility

OpenCode natively reads CLAUDE.md files when no AGENTS.md is present. Confirmed
via POC -- the profile system's CLAUDE.md files work without changes.

### System Prompt Injection

Claude Code uses `--append-system-prompt-file` to inject the on_init prompt.
OpenCode has no CLI equivalent. Options:

1. **`instructions` config key** -- point to additional context files via `opencode.json`
2. **Merge into CLAUDE.md** -- combine system prompt with profile CLAUDE.md
3. **Prepend to task description** -- include instructions in the prompt itself

### MCP Server Registration

```bash
# Claude Code
claude mcp add --transport http orchestrator http://localhost:19090/mcp --scope user

# OpenCode -- via opencode.json config file
cat > opencode.json << 'EOF'
{
  "mcp": {
    "orchestrator": {
      "type": "remote",
      "url": "http://localhost:19090/mcp"
    }
  }
}
EOF
```

OpenCode uses config-based MCP registration rather than a CLI `add` command in
non-interactive mode.

### Model Configuration

OpenCode supports Anthropic models via API key (not OAuth):

```bash
# Via environment
export ANTHROPIC_API_KEY="..."

# Via config
{"provider":{"anthropic":{"options":{"apiKey":"{env:ANTHROPIC_API_KEY}"}}}}
```

The Vertex AI proxy approach used by the orchestrator should work with OpenCode
by setting `ANTHROPIC_BASE_URL` for the API endpoint.

## Approach 1: Direct Replacement

Replace Claude Code with OpenCode everywhere. This is the simpler approach but
burns the bridge back to Claude Code.

### Changes Required

#### orchestrator

| File | Change |
|------|--------|
| `internal/cmd/task.go:buildRunScript()` | Replace `claude` invocation with `opencode run` and map flags |
| `internal/cmd/task.go:setupSandbox()` | Replace `claude mcp add` with writing `opencode.json` config |
| `internal/cmd/log.go:formatTranscriptLine()` | Rewrite parser for OpenCode JSON format |
| `internal/task/usage.go` | Update `ParseTranscriptUsage()` for OpenCode event types |
| `internal/sandbox/podman.go` | Change installation from Claude Code to OpenCode |
| `internal/profile/apply.go` | Replace `claude mcp add` with OpenCode config, rename settings path |
| `internal/profile/profile.go` | Keep CLAUDE.md (OpenCode reads it natively) |
| `configs/sandbox.tf` | Change install command |
| `dashboard/app.js` | Update transcript parsing for OpenCode JSON events |
| `internal/cmd/root.go` | Update description text |
| `integration_test.go` | Update test transcript format |

#### gjoll

| File | Change |
|------|--------|
| `examples/ubuntu-claude.tf` | Replace Claude Code install with OpenCode |
| `examples/ubuntu-claude-vertex.tf` | Replace Claude Code config with OpenCode config |
| `examples/fedora-libvirt-vertex.tf` | Replace Claude Code config with OpenCode config |
| `internal/cmd/root.go` | Update description |
| `README.md` | Update documentation |

#### drellaos

No direct changes needed. The OS image doesn't install Claude Code -- the
orchestrator handles installation in each sandbox. However, if we want OpenCode
pre-installed in the OS for interactive use, we'd add `opencode` to the package
list or add an install step.

### Pros
- Simpler codebase, no abstraction layer
- Cleaner migration with no dual-path maintenance

### Cons
- No rollback path
- Can't A/B test the two agents
- All-or-nothing migration

### Example: buildRunScript After Migration

```go
func buildRunScript(taskDescription string, continueSession bool) string {
    escapedDesc := strings.ReplaceAll(taskDescription, "'", "'\\''")

    var flags string
    var teeFlag string
    if continueSession {
        flags = "--continue"
        teeFlag = "-a"
    }

    return fmt.Sprintf(`#!/bin/bash
source ~/.bashrc
export PATH="$HOME/.opencode/bin:$PATH"
stdbuf -oL opencode run --dangerously-skip-permissions \
  --variant max \
  --format json \
  %s '%s' \
  </dev/null | stdbuf -oL tee %s ~/transcript.jsonl
`, flags, escapedDesc, teeFlag)
}
```

## Approach 2: Configurable Agent Backend

Add a configuration option to choose between Claude Code and OpenCode. The
orchestrator abstracts the differences behind an `AgentBackend` interface.

### Design

```go
// AgentBackend defines how to install, configure, and invoke a coding agent.
type AgentBackend interface {
    // Name returns the backend identifier ("claude-code" or "opencode").
    Name() string

    // InstallCmd returns the shell command to install the agent in a sandbox.
    InstallCmd() string

    // BuildRunScript builds the shell script that invokes the agent.
    BuildRunScript(taskDescription string, continueSession bool) string

    // SetupMCP returns shell commands to register an MCP server.
    SetupMCP(name, transport, url string) []string

    // ConfigDir returns the agent's config directory (e.g. ".claude" or ".opencode").
    ConfigDir() string

    // WriteConfig writes agent-specific configuration files to the sandbox.
    WriteConfig(ctx context.Context, runner sandbox.Runner, sbx string, opts ConfigOpts) error

    // ParseTranscriptUsage extracts usage data from the agent's transcript format.
    ParseTranscriptUsage(path string) (*task.Usage, error)

    // FormatTranscriptLine formats a JSONL line for human-readable display.
    FormatTranscriptLine(line []byte, verbose bool) string
}
```

### Configuration

```yaml
# orchestrator.yaml
agent_backend: opencode  # or "claude-code" (default)
```

### Changes Required

| File | Change |
|------|--------|
| `internal/agent/backend.go` | New: `AgentBackend` interface |
| `internal/agent/claudecode.go` | New: Claude Code implementation (extract from current code) |
| `internal/agent/opencode.go` | New: OpenCode implementation |
| `internal/config/config.go` | Add `AgentBackend` config field |
| `internal/cmd/task.go` | Use `AgentBackend` instead of hardcoded Claude commands |
| `internal/cmd/log.go` | Delegate to backend's transcript formatter |
| `internal/task/usage.go` | Delegate to backend's usage parser |
| `internal/profile/apply.go` | Use backend for MCP registration and config paths |
| `internal/sandbox/podman.go` | Use backend's install command |

### Pros
- Gradual migration: test OpenCode on select tasks while keeping Claude Code as default
- Easy rollback per-task or globally
- Clean separation of concerns
- Enables future backends (Cursor Agent, Codex, etc.)

### Cons
- More code to maintain
- Abstraction adds complexity
- Both backends must be kept working

### Example: Backend Implementations

```go
// claudecode.go
type ClaudeCodeBackend struct{}

func (b *ClaudeCodeBackend) InstallCmd() string {
    return "curl -fsSL https://claude.ai/install.sh | bash"
}

func (b *ClaudeCodeBackend) BuildRunScript(desc string, cont bool) string {
    // Current implementation, extracted
}

func (b *ClaudeCodeBackend) SetupMCP(name, transport, url string) []string {
    return []string{
        fmt.Sprintf("claude mcp add --transport %s %s %s --scope user", transport, name, url),
    }
}

// opencode.go
type OpenCodeBackend struct{}

func (b *OpenCodeBackend) InstallCmd() string {
    return "curl -fsSL https://opencode.ai/install | bash"
}

func (b *OpenCodeBackend) BuildRunScript(desc string, cont bool) string {
    // OpenCode equivalent
}

func (b *OpenCodeBackend) SetupMCP(name, transport, url string) []string {
    // Write opencode.json with MCP config
    return []string{
        fmt.Sprintf(`mkdir -p .opencode && cat > opencode.json << 'MCPEOF'
{"mcp":{"%s":{"type":"remote","url":"%s"}}}
MCPEOF`, name, url),
    }
}
```

## Recommendation

**Start with Approach 2** (configurable backend). It allows testing OpenCode on
individual tasks via `--agent-backend opencode` without disrupting existing
workflows. Once confidence is established, flip the default to OpenCode and
eventually remove the Claude Code backend (converging to Approach 1).

## Key Risks

1. **Transcript format** -- The dashboard and `orchestrator log` both parse
   Claude Code's `stream-json` format. OpenCode's JSON events have a completely
   different structure. This is the largest migration surface.

2. **System prompt injection** -- OpenCode lacks `--append-system-prompt-file`.
   Must merge system prompts into CLAUDE.md or use the `instructions` config key.
   This changes the profile application flow.

3. **Vertex AI proxy** -- The orchestrator's proxy-based credential injection
   should work since OpenCode supports `ANTHROPIC_BASE_URL`, but this needs
   end-to-end testing with actual API calls.

4. **MCP tool compatibility** -- The orchestrator's MCP tools (open_pr, update_pr,
   comment_on_pr, post_review) use the standard MCP protocol. OpenCode supports
   MCP, so these should work, but the registration mechanism is different
   (config file vs CLI command).

5. **Conversation archival** -- The orchestrator archives `~/.claude/` after each
   run. OpenCode stores state in `~/.opencode/` (or project-level `.opencode/`).
   The archival path needs updating.
