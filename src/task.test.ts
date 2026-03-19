import { describe, it, expect } from "vitest";
import * as fs from "node:fs";
import * as path from "node:path";
import * as os from "node:os";
import {
  create,
  open,
  transcriptPathFor,
  prNumberFromURL,
  type PR,
} from "./task.js";

function tmpDir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "task-test-"));
}

describe("create", () => {
  it("creates directories", () => {
    const dir = tmpDir();
    const td = create(dir, "my-task");
    expect(fs.existsSync(td.repoPath())).toBe(true);
    expect(fs.existsSync(td.conversationsPath())).toBe(true);
    expect(td.repoPath()).toBe(path.join(dir, "my-task", "repo"));
    expect(td.conversationsPath()).toBe(
      path.join(dir, "my-task", "conversations")
    );
    expect(td.transcriptPath()).toBe(
      path.join(dir, "my-task", "transcript.jsonl")
    );
  });

  it("fails if already exists", () => {
    const dir = tmpDir();
    fs.mkdirSync(path.join(dir, "existing-task"), { recursive: true });
    expect(() => create(dir, "existing-task")).toThrow("already exists");
  });
});

describe("open", () => {
  it("opens existing task", () => {
    const dir = tmpDir();
    create(dir, "my-task");
    const td = open(dir, "my-task");
    expect(td.repoPath()).toBe(path.join(dir, "my-task", "repo"));
  });

  it("fails if not exists", () => {
    const dir = tmpDir();
    expect(() => open(dir, "nonexistent")).toThrow("does not exist");
  });
});

describe("transcriptPathFor", () => {
  it("returns correct path", () => {
    const got = transcriptPathFor("/output", "my-task");
    expect(got).toBe(path.join("/output", "my-task", "transcript.jsonl"));
  });
});

describe("saveMetadata", () => {
  it("saves and loads metadata", () => {
    const dir = tmpDir();
    const td = create(dir, "meta-test");
    const now = new Date();
    td.saveMetadata("meta-test", "test task description", "", now);

    const s = td.loadState();
    expect(s.name).toBe("meta-test");
    expect(s.description).toBe("test task description");
  });

  it("sets updated_at", () => {
    const dir = tmpDir();
    const td = create(dir, "updated-test");
    const now = new Date();
    td.saveMetadata("updated-test", "desc", "", now);

    const s = td.loadState();
    expect(s.updated_at).toBe(now.toISOString());
  });

  it("saves author", () => {
    const dir = tmpDir();
    const td = create(dir, "author-test");
    td.saveMetadata(
      "author-test",
      "test task",
      "Jane Doe <jane@example.com>",
      new Date()
    );

    const s = td.loadState();
    expect(s.author).toBe("Jane Doe <jane@example.com>");
  });

  it("omits empty author", () => {
    const dir = tmpDir();
    const td = create(dir, "no-author-test");
    td.saveMetadata("no-author-test", "test task", "", new Date());

    const data = fs.readFileSync(
      path.join(dir, "no-author-test", "state.json"),
      "utf-8"
    );
    expect(data).not.toContain('"author"');
  });

  it("preserves existing state", () => {
    const dir = tmpDir();
    const td = create(dir, "preserve-test");
    td.addPR({
      url: "https://github.com/org/repo/pull/1",
      repo: "org/repo",
      branch: "fix",
      base: "main",
    });
    td.saveMetadata("preserve-test", "desc", "", new Date());

    const s = td.loadState();
    expect(s.name).toBe("preserve-test");
    expect(s.resources.github.prs).toHaveLength(1);
  });
});

describe("touchUpdatedAt", () => {
  it("updates the timestamp", () => {
    const dir = tmpDir();
    const td = create(dir, "touch-test");
    const created = new Date(Date.now() - 3600000);
    td.saveMetadata("touch-test", "desc", "", created);

    const updated = new Date();
    td.touchUpdatedAt(updated);

    const s = td.loadState();
    expect(s.created_at).toBe(created.toISOString());
    expect(s.updated_at).toBe(updated.toISOString());
  });

  it("preserves state", () => {
    const dir = tmpDir();
    const td = create(dir, "touch-preserve");
    td.saveMetadata("touch-preserve", "my desc", "author", new Date());
    td.addPR({
      url: "https://github.com/org/repo/pull/1",
      repo: "org/repo",
      branch: "fix",
      base: "main",
    });

    td.touchUpdatedAt(new Date());

    const s = td.loadState();
    expect(s.name).toBe("touch-preserve");
    expect(s.description).toBe("my desc");
    expect(s.author).toBe("author");
    expect(s.resources.github.prs).toHaveLength(1);
  });
});

describe("loadState", () => {
  it("returns empty state when no file", () => {
    const dir = tmpDir();
    const td = create(dir, "state-test");
    const s = td.loadState();
    expect(s.resources.github.prs).toHaveLength(0);
  });
});

describe("prNumberFromURL", () => {
  it("standard PR URL", () => {
    expect(prNumberFromURL("https://github.com/org/repo/pull/42")).toBe(42);
  });

  it("trailing slash", () => {
    expect(prNumberFromURL("https://github.com/org/repo/pull/99/")).toBe(99);
  });

  it("sub-path", () => {
    expect(prNumberFromURL("https://github.com/org/repo/pull/7/files")).toBe(
      7
    );
  });

  it("no pull path throws", () => {
    expect(() =>
      prNumberFromURL("https://github.com/org/repo/issues/5")
    ).toThrow();
  });

  it("non-numeric throws", () => {
    expect(() =>
      prNumberFromURL("https://github.com/org/repo/pull/abc")
    ).toThrow();
  });
});

describe("updatePR", () => {
  it("updates LastCommentID", () => {
    const dir = tmpDir();
    const td = create(dir, "update-test");
    td.addPR({
      url: "https://github.com/org/repo/pull/1",
      repo: "org/repo",
      branch: "fix",
      base: "main",
    });

    td.updatePR("https://github.com/org/repo/pull/1", (pr) => {
      pr.last_comment_id = 42;
    });

    const state = td.loadState();
    expect(state.resources.github.prs[0].last_comment_id).toBe(42);
  });

  it("marks closed", () => {
    const dir = tmpDir();
    const td = create(dir, "close-test");
    td.addPR({
      url: "https://github.com/org/repo/pull/1",
      repo: "org/repo",
      branch: "fix",
      base: "main",
    });

    td.updatePR("https://github.com/org/repo/pull/1", (pr) => {
      pr.closed = true;
    });

    const state = td.loadState();
    expect(state.resources.github.prs[0].closed).toBe(true);
  });

  it("throws for not found", () => {
    const dir = tmpDir();
    const td = create(dir, "notfound-test");
    td.addPR({
      url: "https://github.com/org/repo/pull/1",
      repo: "org/repo",
      branch: "fix",
      base: "main",
    });

    expect(() =>
      td.updatePR("https://github.com/org/repo/pull/999", () => {})
    ).toThrow("PR not found");
  });
});

describe("addPR", () => {
  it("populates number from URL", () => {
    const dir = tmpDir();
    const td = create(dir, "number-test");
    td.addPR({
      url: "https://github.com/org/repo/pull/42",
      repo: "org/repo",
      branch: "fix",
      base: "main",
    });

    const state = td.loadState();
    expect(state.resources.github.prs[0].number).toBe(42);
  });

  it("adds multiple PRs", () => {
    const dir = tmpDir();
    const td = create(dir, "pr-test");

    td.addPR({
      url: "https://github.com/org/repo/pull/1",
      repo: "org/repo",
      branch: "fix-bug",
      base: "main",
    });
    td.addPR({
      url: "https://github.com/org/repo/pull/2",
      repo: "org/repo",
      branch: "add-feature",
      base: "main",
    });

    const state = td.loadState();
    expect(state.resources.github.prs).toHaveLength(2);

    // Verify on-disk JSON structure
    const data = JSON.parse(
      fs.readFileSync(path.join(dir, "pr-test", "state.json"), "utf-8")
    );
    expect(data.resources.github.prs).toHaveLength(2);
  });
});
