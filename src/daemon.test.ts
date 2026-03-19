import { describe, it, expect } from "vitest";
import * as fs from "node:fs";
import * as path from "node:path";
import * as os from "node:os";
import {
  filterNewComments,
  formatCommentsAsPrompt,
  discoverPRs,
  Daemon,
} from "./daemon.js";
import * as task from "./task.js";
import type { Comment } from "./github.js";

function tmpDir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "daemon-test-"));
}

describe("filterNewComments", () => {
  const comments: Comment[] = [
    {
      id: 1,
      body: "old",
      user: { login: "alice" },
      created_at: "2025-01-01T00:00:00Z",
    },
    {
      id: 5,
      body: "new from alice",
      user: { login: "alice" },
      created_at: "2025-01-02T00:00:00Z",
    },
    {
      id: 10,
      body: "from bob",
      user: { login: "bob" },
      created_at: "2025-01-03T00:00:00Z",
    },
    {
      id: 15,
      body: "from eve",
      user: { login: "eve" },
      created_at: "2025-01-04T00:00:00Z",
    },
  ];

  it("filters by lastCommentID and allowed users", () => {
    const result = filterNewComments(comments, 3, ["alice", "bob"]);
    expect(result).toHaveLength(2);
    expect(result[0].id).toBe(5);
    expect(result[1].id).toBe(10);
  });

  it("returns empty when no new comments", () => {
    const result = filterNewComments(comments, 20, ["alice"]);
    expect(result).toHaveLength(0);
  });

  it("returns empty when user not allowed", () => {
    const result = filterNewComments(comments, 0, ["nobody"]);
    expect(result).toHaveLength(0);
  });
});

describe("formatCommentsAsPrompt", () => {
  it("formats single comment", () => {
    const comments: Comment[] = [
      {
        id: 1,
        body: "Please fix the bug",
        user: { login: "alice" },
        created_at: "2025-01-01T12:00:00Z",
      },
    ];
    const prompt = formatCommentsAsPrompt(comments);
    expect(prompt).toContain("@alice at 2025-01-01T12:00:00Z");
    expect(prompt).toContain("Please fix the bug");
  });

  it("formats review comment with path", () => {
    const comments: Comment[] = [
      {
        id: 1,
        body: "Fix this line",
        user: { login: "bob" },
        created_at: "2025-01-01T00:00:00Z",
        type: "review",
        path: "src/main.ts",
      },
    ];
    const prompt = formatCommentsAsPrompt(comments);
    expect(prompt).toContain("on src/main.ts");
  });

  it("formats multiple comments with separator", () => {
    const comments: Comment[] = [
      {
        id: 1,
        body: "first",
        user: { login: "a" },
        created_at: "2025-01-01T00:00:00Z",
      },
      {
        id: 2,
        body: "second",
        user: { login: "b" },
        created_at: "2025-01-02T00:00:00Z",
      },
    ];
    const prompt = formatCommentsAsPrompt(comments);
    expect(prompt).toContain("---");
  });

  it("sorts comments by ID", () => {
    const comments: Comment[] = [
      {
        id: 20,
        body: "later",
        user: { login: "a" },
        created_at: "2025-01-02T00:00:00Z",
      },
      {
        id: 10,
        body: "earlier",
        user: { login: "b" },
        created_at: "2025-01-01T00:00:00Z",
      },
    ];
    const prompt = formatCommentsAsPrompt(comments);
    const earlierIdx = prompt.indexOf("earlier");
    const laterIdx = prompt.indexOf("later");
    expect(earlierIdx).toBeLessThan(laterIdx);
  });
});

describe("discoverPRs", () => {
  it("returns empty for non-existent dir", () => {
    expect(discoverPRs("/nonexistent")).toEqual([]);
  });

  it("discovers open PRs from tasks", () => {
    const dir = tmpDir();
    const td = task.create(dir, "test-task");
    td.addPR({
      url: "https://github.com/org/repo/pull/42",
      repo: "org/repo",
      branch: "fix",
      base: "main",
    });

    const refs = discoverPRs(dir);
    expect(refs).toHaveLength(1);
    expect(refs[0].taskName).toBe("test-task");
    expect(refs[0].pr.number).toBe(42);
  });

  it("skips closed PRs", () => {
    const dir = tmpDir();
    const td = task.create(dir, "closed-task");
    td.addPR({
      url: "https://github.com/org/repo/pull/1",
      repo: "org/repo",
      branch: "fix",
      base: "main",
    });
    td.updatePR("https://github.com/org/repo/pull/1", (pr) => {
      pr.closed = true;
    });

    const refs = discoverPRs(dir);
    expect(refs).toHaveLength(0);
  });
});

describe("Daemon", () => {
  it("tracks running state", () => {
    const d = new Daemon(
      null as any,
      1000,
      "/config",
      "/output",
      []
    );
    expect(d.isTaskRunning("test")).toBe(false);
    d.setTaskRunning("test", true);
    expect(d.isTaskRunning("test")).toBe(true);
    d.setTaskRunning("test", false);
    expect(d.isTaskRunning("test")).toBe(false);
  });
});
