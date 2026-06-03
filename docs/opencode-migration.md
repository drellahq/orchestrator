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

## Approach 2: Configurable Agent Backend (Detailed Spec)

Add a configuration option to choose between Claude Code and OpenCode. The
orchestrator abstracts the differences behind an `agent.Backend` interface,
allowing per-task or global switching between agents with a safe rollback path.

### Interface

The `agent.Backend` interface is defined in `internal/agent/backend.go`:

```go
type Backend interface {
    Name() string
    InstallCmd() string
    BinaryPath() string
    BuildRunScript(taskDescription string, continueSession bool, systemPromptFile string) string
    MCPAddCmd(name, transport, url, scope string) string
    ConfigDir() string
    ConversationDir() string
    FormatTranscriptLine(line []byte, verbose bool) string
    ParseResultEntry(line []byte) *UsageEntry
}
```

Method summary:

| Method | Purpose |
|--------|---------|
| `Name()` | Returns `"claude-code"` or `"opencode"` |
| `InstallCmd()` | Shell command to install the agent binary |
| `BinaryPath()` | PATH export needed to find the binary |
| `BuildRunScript()` | Generates the headless execution shell script |
| `MCPAddCmd()` | Shell command to register an MCP server |
| `ConfigDir()` | Agent's config directory name (`.claude` or `.opencode`) |
| `ConversationDir()` | Directory to archive after a run |
| `FormatTranscriptLine()` | Formats a JSONL line for human-readable display |
| `ParseResultEntry()` | Extracts usage data from a JSONL result line |

The factory function `agent.New(name)` returns the appropriate backend:

```go
func New(name string) (Backend, error) {
    switch name {
    case "", "claude-code":
        return &claudeCode{}, nil
    case "opencode":
        return &openCode{}, nil
    default:
        return nil, fmt.Errorf("unknown agent backend %q (valid: claude-code, opencode)", name)
    }
}
```

### Configuration

```yaml
# orchestrator.yaml
agent_backend: opencode  # or "claude-code" (default)
```

The `Config` struct gains one field:

```go
type Config struct {
    // ...existing fields...
    AgentBackend string `yaml:"agent_backend"` // "claude-code" (default) or "opencode"
}
```

Default: `"claude-code"` (empty string also resolves to claude-code), preserving
backward compatibility with existing deployments.

### Integration Points

Each integration point describes what changes and why, with before/after code
where the diff is non-obvious.

#### 1. Task execution (`internal/cmd/task.go`)

**What changes:** `executeTask()` creates an `agent.Backend` from config and
threads it through all sandbox setup and execution steps. The local
`buildRunScript()` function is removed — its logic lives in the backend.

The backend is created once at the top of `executeTask()`:

```go
backend, err := agent.New(cfg.AgentBackend)
if err != nil {
    return fmt.Errorf("creating agent backend: %w", err)
}
```

**Run script generation.** The hardcoded `buildRunScript()` is replaced by
`backend.BuildRunScript(desc, continueSession, systemPromptFile)`:

| Aspect | Claude Code | OpenCode |
|--------|-------------|----------|
| Binary | `claude -p` | `opencode run` |
| JSON output | `--output-format stream-json` | `--format json` |
| Effort | `--effort max` | `--variant max` |
| System prompt | `--append-system-prompt-file ~/system-prompt.md` | *(ignored — merged into CLAUDE.md)* |
| API key | Reads `~/.anthropic/api_key` via env export | Not needed (uses `ANTHROPIC_API_KEY` from env or config) |

**MCP registration.** The hardcoded `claude mcp add` command in `setupSandbox()`
is replaced by `backend.MCPAddCmd(name, transport, url, scope)`:

```go
// Before:
mcpCmd := fmt.Sprintf("claude mcp add --transport http orchestrator %s --scope user", mcpURL)

// After:
mcpCmd := backend.MCPAddCmd("orchestrator", "http", mcpURL, "user")
```

For Claude Code, this produces the same `claude mcp add` command. For OpenCode,
it writes an `opencode.json` config file with the MCP server definition.

**Conversation archival.** The hardcoded `~/.claude/` archival path is replaced
by `backend.ConversationDir()`:

```go
// Before:
runner.Cp(ctx, taskName, taskName+":"+home+"/.claude/", taskDir.ConversationsPath())

// After:
runner.Cp(ctx, taskName, taskName+":"+home+"/"+backend.ConversationDir()+"/", taskDir.ConversationsPath())
```

**System prompt handling.** This is the most significant behavioral difference
between the two backends:

- **Claude Code:** The `on_init` prompt is written to `~/system-prompt.md` and
  injected via `--append-system-prompt-file`. The profile's CLAUDE.md is written
  to `~/.claude/CLAUDE.md` separately.

- **OpenCode:** There is no `--append-system-prompt-file` flag. Instead, the
  `on_init` prompt is merged into the CLAUDE.md file that OpenCode reads natively.
  When a profile is active, the combined content is:
  `base prompt + profile CLAUDE.md + on_init prompt`. Without a profile:
  `on_init prompt` is written directly to `~/.claude/CLAUDE.md` (which OpenCode
  reads as its equivalent of CLAUDE.md).

The `setupSandboxDefault()` and `setupSandboxWithProfile()` functions check
`backend.Name()` to decide the strategy:

```go
// For claude-code: write system-prompt.md (used by --append-system-prompt-file)
// For opencode: append on_init to CLAUDE.md (no CLI flag equivalent)
if backend.Name() == "opencode" {
    // Merge on_init into CLAUDE.md
    writeToSandbox(ctx, runner, sbx, prompts.OnInit, sbx+":"+home+"/.claude/CLAUDE.md")
} else {
    // Write separate system-prompt.md
    copyToSandbox(ctx, runner, sbx, prompts.OnInit, home+"/system-prompt.md")
}
```

#### 2. Transcript formatting (`internal/cmd/log.go`)

**What changes:** The local `formatTranscriptLine()`, `toolInputSummary()`,
`formatTokenCount()`, and `firstLine()` functions are removed. Their logic now
lives in the backend implementations and shared helpers in `internal/agent/`.

The `transcriptWriter` and `runLog` both delegate to `backend.FormatTranscriptLine()`:

```go
// transcriptWriter gains a backend field
type transcriptWriter struct {
    w       io.Writer
    buf     []byte
    verbose bool
    backend agent.Backend
}

// runLog creates the backend from config and uses it for both local and live logs
func runLog(cmd *cobra.Command, args []string) error {
    cfg, err := config.Load(configPath)
    backend, err := agent.New(cfg.AgentBackend)
    // ...
    scanner := bufio.NewScanner(f)
    for scanner.Scan() {
        formatted := backend.FormatTranscriptLine(scanner.Bytes(), verbose)
        if formatted != "" {
            fmt.Print(formatted)
        }
    }
}
```

Both backends produce the same human-readable output format (`[tool]`, `[result]`,
`[thinking]` prefixes) so the user experience is consistent regardless of which
agent produced the transcript.

#### 3. Usage parsing (`internal/task/usage.go`)

**What changes:** `ParseTranscriptUsage()` gains a `backend agent.Backend`
parameter and delegates per-line parsing to `backend.ParseResultEntry()`:

```go
func ParseTranscriptUsage(path string, backend agent.Backend) (*Usage, error) {
    // ...
    for scanner.Scan() {
        entry := backend.ParseResultEntry(scanner.Bytes())
        if entry == nil {
            continue
        }
        total.CostUSD += entry.CostUSD
        total.InputTokens += entry.InputTokens
        total.OutputTokens += entry.OutputTokens
    }
    // ...
}
```

The key difference: Claude Code emits `{"type":"result"}` with aggregated usage,
while OpenCode emits `{"type":"step_finish","part":{"reason":"stop"}}` with
per-step usage. The backend's `ParseResultEntry()` handles this mapping.

#### 4. Profile system (`internal/profile/apply.go`)

**What changes:** The `Apply()` function gains a `backend agent.Backend`
parameter. Two callsites change:

**Config directory.** The hardcoded `.claude` path in `writeToSandbox()` and
`mkdir` calls is replaced by `backend.ConfigDir()`:

```go
// Before:
runner.SSH(ctx, sbx, "bash", "-c", runner.AsUser("mkdir -p ~/.claude"))
writeToSandbox(ctx, runner, sbx, claudemd, sbx+":"+home+"/.claude/CLAUDE.md")

// After:
configDir := backend.ConfigDir()
runner.SSH(ctx, sbx, "bash", "-c", runner.AsUser("mkdir -p ~/"+configDir))
writeToSandbox(ctx, runner, sbx, claudemd, sbx+":"+home+"/"+configDir+"/CLAUDE.md")
```

**MCP registration.** The `registerMCPServer()` function replaces the hardcoded
`claude mcp add` construction with `backend.MCPAddCmd()`:

```go
// Before:
args = []string{"claude", "mcp", "add", "--transport", "http", ...}
innerCmd := strings.Join(args, " ")

// After:
innerCmd := backend.MCPAddCmd(server.Name, server.Transport, server.URL, server.Scope)
```

For stdio-transport MCP servers under OpenCode, the config-file approach supports
local process execution via `{"type":"local","command":"...","args":[...]}`.

#### 5. Sandbox provisioning (`internal/sandbox/podman.go`)

**What changes:** The hardcoded `curl -fsSL https://claude.ai/install.sh | bash`
in `PodmanRunner.Up()` is replaced by an injected install command.

`NewFromConfig()` in `sandbox.go` accepts the agent install command and passes
it to `NewPodman()`:

```go
func NewFromConfig(backend, gjollEnv, podmanImage, anthropicKeyFile string,
    mcpPort int, agentInstallCmd string) (Runner, error) {
    switch backend {
    case "podman":
        return NewPodman(podmanImage, anthropicKeyFile, mcpPort, agentInstallCmd), nil
    // ...
    }
}
```

For gjoll-based sandboxes, the agent installation happens in the `.tf` file's
`init_script` output, which is outside the orchestrator's control. Gjoll `.tf`
files must be updated separately (see **gjoll changes** below).

#### 6. Dashboard (`dashboard/app.js`)

**What changes:** The dashboard auto-detects the transcript format. Both Claude
Code and OpenCode transcripts are JSONL, but the event types differ. The
detection heuristic checks the first parseable event:

- If it has `type: "system"` and `subtype: "init"` → Claude Code format
- If it has `type: "step_start"` or `sessionID` field → OpenCode format

The `renderEntry()` function dispatches to format-specific renderers:

```javascript
function renderEntry(entry) {
    // OpenCode events
    if (entry.sessionID !== undefined || entry.part !== undefined) {
        return renderOpenCodeEntry(entry);
    }
    // Claude Code events (existing logic)
    return renderClaudeCodeEntry(entry);
}
```

OpenCode event mapping to the existing UI components:

| OpenCode event | Renders as |
|----------------|------------|
| `step_start` | System init banner |
| `text` | Assistant text block |
| `tool_use` | Tool call with input/output |
| `thinking` | Collapsible thinking block |
| `step_finish` (reason: stop) | Result summary with cost/tokens |

### gjoll changes

The gjoll repository's example `.tf` files hardcode Claude Code installation in
their `init_script` outputs. These need companion OpenCode variants:

| File | Change |
|------|--------|
| `examples/ubuntu-claude.tf` | Rename to `ubuntu-claude-code.tf` |
| `examples/ubuntu-opencode.tf` | New: OpenCode variant of the Ubuntu example |
| `examples/ubuntu-claude-vertex.tf` | Rename to `ubuntu-claude-code-vertex.tf` |
| `examples/ubuntu-opencode-vertex.tf` | New: OpenCode + Vertex proxy variant |
| `examples/fedora-libvirt-vertex.tf` | Add commented OpenCode alternative |
| `examples/fedora-libvirt-opencode-vertex.tf` | New: OpenCode variant for libvirt |
| `internal/cmd/root.go` | Update description to mention multiple coding agents |
| `README.md` | Document OpenCode as an alternative agent |

The OpenCode `.tf` variants differ in:
1. **Install command:** `curl -fsSL https://opencode.ai/install | bash` instead
   of the Claude Code npm install
2. **Binary path:** `~/.opencode/bin/opencode` instead of npm global
3. **No Node.js dependency:** OpenCode is a standalone binary (Go), eliminating
   the Node.js 22 install step
4. **Environment variables:** `ANTHROPIC_BASE_URL` instead of Claude Code's
   `ANTHROPIC_VERTEX_BASE_URL` / `CLAUDE_CODE_USE_VERTEX` for Vertex AI proxy

### Full change manifest

#### orchestrator

| File | Change | Lines (est.) |
|------|--------|-------------|
| `internal/agent/backend.go` | Already exists on branch | 0 |
| `internal/agent/claudecode.go` | Already exists on branch | 0 |
| `internal/agent/opencode.go` | Already exists on branch | 0 |
| `internal/agent/backend_test.go` | Already exists on branch | 0 |
| `internal/config/config.go` | Add `AgentBackend` field | +3 |
| `internal/cmd/task.go` | Use `agent.Backend` for run script, MCP, archival, system prompt | ~+30/−25 |
| `internal/cmd/log.go` | Delegate formatting to backend, remove local copies | ~+15/−120 |
| `internal/task/usage.go` | Accept backend param, delegate to `ParseResultEntry()` | ~+5/−15 |
| `internal/sandbox/sandbox.go` | Accept agent install cmd in `NewFromConfig()` | ~+3/−2 |
| `internal/sandbox/podman.go` | Use injected install command | ~+5/−3 |
| `internal/profile/apply.go` | Accept backend, use for MCP and config dir | ~+15/−10 |
| `dashboard/app.js` | Auto-detect format, add OpenCode event renderers | ~+80/−0 |

#### gjoll

| File | Change |
|------|--------|
| `examples/fedora-libvirt-opencode-vertex.tf` | New: OpenCode libvirt+Vertex example |
| `internal/cmd/root.go` | Update Long description |
| `README.md` | Note OpenCode as alternative |

### Testing strategy

1. **Unit tests** (`internal/agent/backend_test.go`): Already cover both backends
   for `BuildRunScript`, `MCPAddCmd`, `FormatTranscriptLine`, `ParseResultEntry`,
   `ConfigDir`, and `InstallCmd`.

2. **Integration test transcript fixtures**: Add OpenCode-format transcript
   fixtures alongside existing Claude Code fixtures. The `log` and `usage`
   tests should pass for both formats.

3. **Podman sandbox test**: Verify that `PodmanRunner.Up()` installs the correct
   agent when `agent_backend` is set to `"opencode"`.

4. **Profile test**: Verify that `profile.Apply()` uses the correct config
   directory and MCP registration method for each backend.

5. **Dashboard manual test**: Load the dashboard with both Claude Code and
   OpenCode transcript fixtures, verify rendering.

### Migration plan

1. **Phase 1 (this PR):** Ship the `agent.Backend` abstraction and wire it into
   all integration points. Default remains `claude-code`. OpenCode backend is
   available but opt-in via `agent_backend: opencode` in config.

2. **Phase 2 (follow-up):** Run OpenCode on select tasks via config override.
   Validate transcript parsing, MCP tool availability, usage tracking, and
   dashboard rendering with real OpenCode runs.

3. **Phase 3:** Once confidence is established, flip the default to `opencode`
   and add a deprecation notice for the Claude Code backend.

4. **Phase 4:** Remove the Claude Code backend, converging to Approach 1. At
   this point, rename the config field or remove it entirely.

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
