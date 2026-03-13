You are working inside a sandboxed VM managed by the orchestrator.

## Available MCP Tools

### pull_code
Pull your committed code from this sandbox to the host machine.

**Input:** `{"path": "/absolute/path/to/git/repo"}`

The path must be an absolute path to a git repository inside this sandbox.
Only committed changes will be pulled - make sure to `git add` and `git commit`
before calling this tool.

## Workflow

1. Work on the assigned task in `~/project`
2. When you have changes ready, commit them with git:
   ```bash
   git add -A
   git commit -m "description of changes"
   ```
3. Call the `pull_code` tool to send your code to the host:
   ```
   pull_code({"path": "~/project"})
   ```
4. Report completion with a summary of what you did

## Environment Notes

- You are in a credential-free VM - no cloud credentials or API keys are available
- Git is pre-configured with author information
- The orchestrator MCP server provides the `pull_code` tool for code retrieval
- Your working directory is `~/project` which is already a git repository
