import * as http from "node:http";
import * as net from "node:net";
import { minimatch } from "minimatch";
import type { Logger } from "./logging.js";
import type { Dir } from "./task.js";
import type { PR } from "./task.js";

/** The port exposed inside the sandbox VM via SSH reverse tunnel. */
export const MCP_REMOTE_PORT = 19090;

/** CodePuller pulls committed code from a sandbox into a local git repo. */
export interface CodePuller {
  pull(name: string, remotePath: string, localRepoDir: string): void;
}

/** PROpener handles GitHub operations for opening pull requests. */
export interface PROpener {
  authenticatedUser(): string;
  ensureFork(upstream: string): string;
  pushBranch(
    repoDir: string,
    forkFullName: string,
    branch: string,
    sourceRef: string
  ): void;
  createPR(
    upstream: string,
    forkOwner: string,
    branch: string,
    base: string,
    title: string,
    body: string
  ): string;
  addCoAuthorTrailers(
    repoDir: string,
    upstream: string,
    base: string,
    sourceRef: string,
    trailer: string
  ): void;
  commentOnPR(prURL: string, body: string): void;
  updatePRTitle(prURL: string, title: string): void;
}

interface OpenPRInput {
  path: string;
  repo: string;
  branch: string;
  base?: string;
  title: string;
  body: string;
}

interface UpdatePRInput {
  path: string;
  repo: string;
  branch: string;
}

interface CommentOnPRInput {
  pr_url: string;
  body: string;
  title?: string;
}

// JSON-RPC types
interface JsonRpcRequest {
  jsonrpc: string;
  id?: number | string;
  method: string;
  params?: Record<string, unknown>;
}

interface JsonRpcResponse {
  jsonrpc: string;
  id: number | string | null;
  result?: unknown;
  error?: { code: number; message: string };
}

interface ToolResult {
  content: Array<{ type: string; text: string }>;
  isError?: boolean;
}

function isRepoAllowed(repo: string, allowedRepos: string[]): boolean {
  return allowedRepos.some((pattern) => minimatch(repo, pattern));
}

function baseBranchForPR(
  taskDir: Dir,
  repo: string,
  branch: string
): string {
  try {
    const state = taskDir.loadState();
    for (const pr of state.resources.github.prs) {
      if (pr.repo === repo && pr.branch === branch) {
        return pr.base;
      }
    }
  } catch {
    // ignore
  }
  return "main";
}

function resolvePushTarget(
  repo: string,
  prOpener: PROpener
): { pushTarget: string; forkOwner: string } {
  const forkOwner = prOpener.authenticatedUser();
  const repoOwner = repo.split("/")[0];
  let pushTarget = repo;

  if (forkOwner !== repoOwner) {
    pushTarget = prOpener.ensureFork(repo);
  }

  return { pushTarget, forkOwner };
}

function toolOk(text: string): ToolResult {
  return { content: [{ type: "text", text }] };
}

function toolErr(text: string): ToolResult {
  return { content: [{ type: "text", text }], isError: true };
}

const TOOLS = [
  {
    name: "open_pr",
    description:
      "Push committed code from the sandbox and open a draft pull request on GitHub. The tool returns the URL of the created PR.",
    inputSchema: {
      type: "object",
      properties: {
        path: {
          type: "string",
          description: "Absolute path to the git repo in the sandbox",
        },
        repo: {
          type: "string",
          description: "Target repository as owner/repo",
        },
        branch: {
          type: "string",
          description: "Name of the remote branch to push to",
        },
        base: {
          type: "string",
          description: "Base branch for the PR (default: main)",
        },
        title: { type: "string", description: "PR title" },
        body: { type: "string", description: "PR body/description" },
      },
      required: ["path", "repo", "branch", "title", "body"],
    },
  },
  {
    name: "update_pr",
    description: "Push committed code from the sandbox to an existing PR",
    inputSchema: {
      type: "object",
      properties: {
        path: {
          type: "string",
          description: "Absolute path to the git repo in the sandbox",
        },
        repo: {
          type: "string",
          description: "Target repository as owner/repo",
        },
        branch: {
          type: "string",
          description:
            "Name of the remote branch to push to (must match the existing PR branch)",
        },
      },
      required: ["path", "repo", "branch"],
    },
  },
  {
    name: "comment_on_pr",
    description: "Post a comment on a pull request opened by this task",
    inputSchema: {
      type: "object",
      properties: {
        pr_url: {
          type: "string",
          description: "URL of the pull request to comment on",
        },
        body: {
          type: "string",
          description: "Comment body (markdown supported)",
        },
        title: {
          type: "string",
          description: "Optional new title for the PR",
        },
      },
      required: ["pr_url", "body"],
    },
  },
];

export class Server {
  private httpServer: http.Server;
  private listener?: net.Server;
  private sessions = new Map<string, boolean>();

  private taskName: string;
  private taskDir: Dir;
  private puller: CodePuller | null;
  private prOpener: PROpener | null;
  private allowedRepos: string[];
  private author: string;
  private logger: Logger;

  constructor(
    logger: Logger,
    taskName: string,
    taskDir: Dir,
    puller: CodePuller | null,
    prOpener: PROpener | null,
    allowedRepos: string[],
    author: string
  ) {
    this.logger = logger;
    this.taskName = taskName;
    this.taskDir = taskDir;
    this.puller = puller;
    this.prOpener = prOpener;
    this.allowedRepos = allowedRepos;
    this.author = author;

    this.httpServer = http.createServer((req, res) =>
      this.handleRequest(req, res)
    );
  }

  private handleRequest(
    req: http.IncomingMessage,
    res: http.ServerResponse
  ): void {
    if (req.method !== "POST") {
      res.writeHead(405);
      res.end();
      return;
    }

    let body = "";
    req.on("data", (chunk: Buffer) => {
      body += chunk.toString();
    });
    req.on("end", () => {
      try {
        const rpc = JSON.parse(body) as JsonRpcRequest;
        this.handleRPC(rpc, req, res);
      } catch {
        res.writeHead(400);
        res.end(JSON.stringify({ error: "invalid JSON" }));
      }
    });
  }

  private handleRPC(
    rpc: JsonRpcRequest,
    req: http.IncomingMessage,
    res: http.ServerResponse
  ): void {
    switch (rpc.method) {
      case "initialize": {
        const sessionId = crypto.randomUUID();
        this.sessions.set(sessionId, true);
        res.setHeader("Mcp-Session-Id", sessionId);
        this.sendResult(res, rpc.id, {
          protocolVersion: "2025-06-18",
          capabilities: { tools: {} },
          serverInfo: { name: "orchestrator", version: "0.1.0" },
        });
        break;
      }
      case "notifications/initialized":
        res.writeHead(200);
        res.end();
        break;
      case "tools/list":
        this.sendResult(res, rpc.id, { tools: TOOLS });
        break;
      case "tools/call": {
        const params = rpc.params as {
          name: string;
          arguments: Record<string, unknown>;
        };
        const result = this.callTool(params.name, params.arguments);
        this.sendResult(res, rpc.id, result);
        break;
      }
      default:
        this.sendError(res, rpc.id, -32601, `Method not found: ${rpc.method}`);
    }
  }

  private sendResult(
    res: http.ServerResponse,
    id: number | string | undefined | null,
    result: unknown
  ): void {
    const response: JsonRpcResponse = {
      jsonrpc: "2.0",
      id: id ?? null,
      result,
    };
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify(response));
  }

  private sendError(
    res: http.ServerResponse,
    id: number | string | undefined | null,
    code: number,
    message: string
  ): void {
    const response: JsonRpcResponse = {
      jsonrpc: "2.0",
      id: id ?? null,
      error: { code, message },
    };
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify(response));
  }

  private callTool(
    name: string,
    args: Record<string, unknown>
  ): ToolResult {
    if (!this.prOpener || this.allowedRepos.length === 0) {
      return toolErr("PR tools not available");
    }

    switch (name) {
      case "open_pr":
        return this.handleOpenPR(args as unknown as OpenPRInput);
      case "update_pr":
        return this.handleUpdatePR(args as unknown as UpdatePRInput);
      case "comment_on_pr":
        return this.handleCommentOnPR(args as unknown as CommentOnPRInput);
      default:
        return toolErr(`unknown tool: ${name}`);
    }
  }

  private handleOpenPR(input: OpenPRInput): ToolResult {
    this.logger.info("PR open requested", {
      task: this.taskName,
      repo: input.repo,
    });

    if (!isRepoAllowed(input.repo, this.allowedRepos)) {
      this.logger.warn("PR open denied: repo not allowed", {
        task: this.taskName,
        repo: input.repo,
      });
      return toolErr(
        `repo "${input.repo}" is not in the allowed repos list`
      );
    }

    try {
      this.puller!.pull(input.path, input.path, this.taskDir.repoPath());
    } catch (e) {
      this.logger.error("Code pull failed", {
        task: this.taskName,
        error: String(e),
      });
      return toolErr(`open_pr failed: ${e}`);
    }

    const base = input.base || "main";
    const sourceRef = `gjoll-${this.taskName}`;

    if (this.author) {
      const trailer = `Co-authored-by: ${this.author}`;
      try {
        this.prOpener!.addCoAuthorTrailers(
          this.taskDir.repoPath(),
          input.repo,
          base,
          sourceRef,
          trailer
        );
      } catch (e) {
        this.logger.error("Failed to add co-author trailers", {
          task: this.taskName,
          error: String(e),
        });
        return toolErr(`open_pr failed: ${e}`);
      }
    }

    let pushTarget: string;
    let forkOwner: string;
    try {
      ({ pushTarget, forkOwner } = resolvePushTarget(
        input.repo,
        this.prOpener!
      ));
    } catch (e) {
      this.logger.error("Failed to resolve push target", {
        task: this.taskName,
        error: String(e),
      });
      return toolErr(`open_pr failed: ${e}`);
    }

    try {
      this.prOpener!.pushBranch(
        this.taskDir.repoPath(),
        pushTarget,
        input.branch,
        sourceRef
      );
    } catch (e) {
      this.logger.error("Failed to push branch", {
        task: this.taskName,
        error: String(e),
      });
      return toolErr(`open_pr failed: ${e}`);
    }

    let prURL: string;
    try {
      prURL = this.prOpener!.createPR(
        input.repo,
        forkOwner,
        input.branch,
        base,
        input.title,
        input.body
      );
    } catch (e) {
      this.logger.error("Failed to create PR", {
        task: this.taskName,
        error: String(e),
      });
      return toolErr(`open_pr failed: ${e}`);
    }

    try {
      this.taskDir.addPR({
        url: prURL,
        repo: input.repo,
        branch: input.branch,
        base,
      } as PR);
    } catch (e) {
      this.logger.warn("Failed to record PR in task state", {
        task: this.taskName,
        error: String(e),
      });
    }

    this.logger.info("PR created", {
      task: this.taskName,
      url: prURL,
      repo: input.repo,
    });
    return toolOk(prURL);
  }

  private handleUpdatePR(input: UpdatePRInput): ToolResult {
    this.logger.info("PR update requested", {
      task: this.taskName,
      repo: input.repo,
      branch: input.branch,
    });

    if (!isRepoAllowed(input.repo, this.allowedRepos)) {
      return toolErr(
        `repo "${input.repo}" is not in the allowed repos list`
      );
    }

    try {
      this.puller!.pull(input.path, input.path, this.taskDir.repoPath());
    } catch (e) {
      return toolErr(`update_pr failed: ${e}`);
    }

    const sourceRef = `gjoll-${this.taskName}`;

    if (this.author) {
      const base = baseBranchForPR(this.taskDir, input.repo, input.branch);
      const trailer = `Co-authored-by: ${this.author}`;
      try {
        this.prOpener!.addCoAuthorTrailers(
          this.taskDir.repoPath(),
          input.repo,
          base,
          sourceRef,
          trailer
        );
      } catch (e) {
        return toolErr(`update_pr failed: ${e}`);
      }
    }

    let pushTarget: string;
    try {
      ({ pushTarget } = resolvePushTarget(input.repo, this.prOpener!));
    } catch (e) {
      return toolErr(`update_pr failed: ${e}`);
    }

    try {
      this.prOpener!.pushBranch(
        this.taskDir.repoPath(),
        pushTarget,
        input.branch,
        sourceRef
      );
    } catch (e) {
      return toolErr(`update_pr failed: ${e}`);
    }

    this.logger.info("Branch updated", {
      task: this.taskName,
      branch: input.branch,
      target: pushTarget,
    });
    return toolOk(
      `Branch ${input.branch} updated on ${pushTarget}. Use \`comment_on_pr\` to post a comment about the changes.`
    );
  }

  private handleCommentOnPR(input: CommentOnPRInput): ToolResult {
    this.logger.info("PR comment requested", {
      task: this.taskName,
      pr_url: input.pr_url,
    });

    const state = this.taskDir.loadState();
    const found = state.resources.github.prs.some(
      (pr) => pr.url === input.pr_url
    );
    if (!found) {
      return toolErr(
        `PR "${input.pr_url}" was not opened by this task`
      );
    }

    try {
      this.prOpener!.commentOnPR(input.pr_url, input.body);
    } catch (e) {
      return toolErr(`comment_on_pr failed: ${e}`);
    }

    if (input.title) {
      try {
        this.prOpener!.updatePRTitle(input.pr_url, input.title);
      } catch (e) {
        return toolErr(
          `comment posted but title update failed: ${e}`
        );
      }
    }

    this.logger.info("PR comment posted", {
      task: this.taskName,
      pr_url: input.pr_url,
    });
    return toolOk(`Comment posted on ${input.pr_url}`);
  }

  /** Starts the MCP server on a dynamically allocated port. */
  start(): Promise<void> {
    return this.startOn("127.0.0.1", 0);
  }

  /** Starts the MCP server on the given address and port. */
  startOn(host: string, port: number): Promise<void> {
    return new Promise((resolve, reject) => {
      this.httpServer.listen(port, host, () => {
        resolve();
      });
      this.httpServer.on("error", reject);
    });
  }

  /** Returns the port the server is listening on. */
  port(): number {
    const addr = this.httpServer.address();
    if (addr && typeof addr === "object") {
      return addr.port;
    }
    return 0;
  }

  /** Gracefully shuts down the MCP server. */
  stop(): Promise<void> {
    return new Promise((resolve) => {
      this.httpServer.close(() => resolve());
    });
  }
}
