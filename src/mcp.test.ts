import { describe, it, expect, afterEach } from "vitest";
import * as http from "node:http";
import * as fs from "node:fs";
import * as path from "node:path";
import * as os from "node:os";
import { Server, type CodePuller, type PROpener } from "./mcp.js";
import * as task from "./task.js";
import type { Logger } from "./logging.js";

function tmpDir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "mcp-test-"));
}

const noopLogger: Logger = {
  debug() {},
  info() {},
  warn() {},
  error() {},
};

function mockPuller(): CodePuller {
  return {
    pull() {},
  };
}

function mockPROpener(user = "testuser"): PROpener {
  return {
    authenticatedUser: () => user,
    ensureFork: (upstream: string) => `${user}/${upstream.split("/")[1]}`,
    pushBranch() {},
    createPR: () => "https://github.com/org/repo/pull/99",
    addCoAuthorTrailers() {},
    commentOnPR() {},
    updatePRTitle() {},
  };
}

async function rpcRequest(
  port: number,
  method: string,
  params?: Record<string, unknown>,
  id = 1
): Promise<{ status: number; body: any }> {
  return new Promise((resolve, reject) => {
    const payload = JSON.stringify({
      jsonrpc: "2.0",
      id,
      method,
      params,
    });

    const req = http.request(
      {
        hostname: "127.0.0.1",
        port,
        path: "/mcp",
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Content-Length": Buffer.byteLength(payload),
        },
      },
      (res) => {
        let body = "";
        res.on("data", (chunk: Buffer) => (body += chunk.toString()));
        res.on("end", () => {
          resolve({
            status: res.statusCode || 0,
            body: body ? JSON.parse(body) : null,
          });
        });
      }
    );
    req.on("error", reject);
    req.write(payload);
    req.end();
  });
}

describe("MCP Server", () => {
  let srv: Server;

  afterEach(async () => {
    if (srv) await srv.stop();
  });

  it("handles initialize", async () => {
    const dir = tmpDir();
    const td = task.create(dir, "init-test");
    srv = new Server(noopLogger, "init-test", td, null, null, [], "");
    await srv.start();
    const port = srv.port();

    const res = await rpcRequest(port, "initialize");
    expect(res.status).toBe(200);
    expect(res.body.result.serverInfo.name).toBe("orchestrator");
    expect(res.body.result.protocolVersion).toBe("2025-06-18");
  });

  it("lists tools", async () => {
    const dir = tmpDir();
    const td = task.create(dir, "tools-test");
    srv = new Server(noopLogger, "tools-test", td, null, null, [], "");
    await srv.start();
    const port = srv.port();

    const res = await rpcRequest(port, "tools/list");
    expect(res.status).toBe(200);
    const tools = res.body.result.tools;
    expect(tools).toHaveLength(3);
    const names = tools.map((t: any) => t.name);
    expect(names).toContain("open_pr");
    expect(names).toContain("update_pr");
    expect(names).toContain("comment_on_pr");
  });

  it("returns error when PR tools unavailable", async () => {
    const dir = tmpDir();
    const td = task.create(dir, "no-tools-test");
    srv = new Server(noopLogger, "no-tools-test", td, null, null, [], "");
    await srv.start();
    const port = srv.port();

    const res = await rpcRequest(port, "tools/call", {
      name: "open_pr",
      arguments: {
        path: "/repo",
        repo: "org/repo",
        branch: "fix",
        title: "test",
        body: "test",
      },
    });
    expect(res.body.result.isError).toBe(true);
    expect(res.body.result.content[0].text).toContain("not available");
  });

  it("denies disallowed repo", async () => {
    const dir = tmpDir();
    const td = task.create(dir, "deny-test");
    srv = new Server(
      noopLogger,
      "deny-test",
      td,
      mockPuller(),
      mockPROpener(),
      ["allowed/repo"],
      ""
    );
    await srv.start();
    const port = srv.port();

    const res = await rpcRequest(port, "tools/call", {
      name: "open_pr",
      arguments: {
        path: "/repo",
        repo: "evil/repo",
        branch: "fix",
        title: "test",
        body: "test",
      },
    });
    expect(res.body.result.isError).toBe(true);
    expect(res.body.result.content[0].text).toContain("not in the allowed");
  });

  it("opens PR successfully", async () => {
    const dir = tmpDir();
    const td = task.create(dir, "open-test");
    srv = new Server(
      noopLogger,
      "open-test",
      td,
      mockPuller(),
      mockPROpener("testuser"),
      ["org/repo"],
      ""
    );
    await srv.start();
    const port = srv.port();

    const res = await rpcRequest(port, "tools/call", {
      name: "open_pr",
      arguments: {
        path: "/repo",
        repo: "org/repo",
        branch: "fix",
        title: "My PR",
        body: "Description",
      },
    });
    expect(res.body.result.isError).toBeUndefined();
    expect(res.body.result.content[0].text).toContain("pull/99");

    // Verify PR was recorded
    const state = td.loadState();
    expect(state.resources.github.prs).toHaveLength(1);
  });

  it("comment_on_pr rejects unknown PR", async () => {
    const dir = tmpDir();
    const td = task.create(dir, "comment-deny-test");
    srv = new Server(
      noopLogger,
      "comment-deny-test",
      td,
      mockPuller(),
      mockPROpener(),
      ["org/repo"],
      ""
    );
    await srv.start();
    const port = srv.port();

    const res = await rpcRequest(port, "tools/call", {
      name: "comment_on_pr",
      arguments: {
        pr_url: "https://github.com/org/repo/pull/999",
        body: "Hello",
      },
    });
    expect(res.body.result.isError).toBe(true);
    expect(res.body.result.content[0].text).toContain("not opened by this task");
  });

  it("returns method not found for unknown RPC", async () => {
    const dir = tmpDir();
    const td = task.create(dir, "unknown-rpc-test");
    srv = new Server(noopLogger, "unknown-rpc-test", td, null, null, [], "");
    await srv.start();
    const port = srv.port();

    const res = await rpcRequest(port, "nonexistent/method");
    expect(res.body.error).toBeDefined();
    expect(res.body.error.code).toBe(-32601);
  });
});
