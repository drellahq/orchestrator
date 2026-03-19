import { execFileSync } from "node:child_process";

export type CommentType = "issue" | "review";

export interface Comment {
  id: number;
  body: string;
  user: { login: string };
  created_at: string;
  type?: CommentType;
  path?: string;
  diff_hunk?: string;
}

/** Runner wraps the gh CLI for GitHub operations. */
export class Runner {
  bin: string;

  constructor(bin?: string) {
    this.bin = bin || "gh";
  }

  /** Returns the login of the currently authenticated GitHub user. */
  authenticatedUser(): string {
    const out = this.runCapture("", this.bin, [
      "api",
      "/user",
      "--jq",
      ".login",
    ]);
    return out.trim();
  }

  /** Ensures a fork of upstream exists for the authenticated user. */
  ensureFork(upstream: string): string {
    const out = this.runCapture("", this.bin, [
      "repo",
      "fork",
      upstream,
      "--clone=false",
      "--default-branch-only",
    ]);

    for (const line of out.split("\n")) {
      const trimmed = line.trim();
      if (trimmed.includes("Created fork ")) {
        return trimmed.replace("Created fork ", "");
      }
      if (trimmed.includes(" already exists")) {
        const parts = trimmed.split(/\s+/);
        if (parts.length > 0) return parts[0];
      }
    }

    // Fallback
    const parts = upstream.split("/");
    if (parts.length !== 2) {
      throw new Error(`cannot determine fork name from output: ${out}`);
    }
    const user = this.authenticatedUser();
    return `${user}/${parts[1]}`;
  }

  /** Adds co-author trailers to commits between upstream/base and sourceRef. */
  addCoAuthorTrailers(
    repoDir: string,
    upstream: string,
    base: string,
    sourceRef: string,
    trailer: string
  ): void {
    const upstreamURL = `https://github.com/${upstream}.git`;
    this.addCoAuthorTrailersInner(
      "git",
      repoDir,
      upstreamURL,
      base,
      sourceRef,
      trailer
    );
  }

  addCoAuthorTrailersInner(
    gitBin: string,
    repoDir: string,
    upstreamURL: string,
    base: string,
    sourceRef: string,
    trailer: string
  ): void {
    const qualifiedRef = `refs/heads/${sourceRef}`;

    // Add upstream remote (or update URL if it exists)
    try {
      this.runCapture(repoDir, gitBin, [
        "remote",
        "add",
        "upstream",
        upstreamURL,
      ]);
    } catch {
      this.runCapture(repoDir, gitBin, [
        "remote",
        "set-url",
        "upstream",
        upstreamURL,
      ]);
    }

    // Fetch the base branch
    this.runCapture(repoDir, gitBin, ["fetch", "upstream", base]);

    // Check if there are any new commits
    const count = this.runCapture(repoDir, gitBin, [
      "rev-list",
      "--count",
      `upstream/${base}..${qualifiedRef}`,
    ]).trim();
    if (count === "0") return;

    // Checkout the sourceRef
    this.runCapture(repoDir, gitBin, [
      "checkout",
      "-B",
      sourceRef,
      qualifiedRef,
    ]);

    // Use git filter-branch to add the trailer
    const msgFilter = `git interpret-trailers --trailer "${trailer}" --if-exists doNothing`;
    execFileSync(
      gitBin,
      [
        "filter-branch",
        "-f",
        "--msg-filter",
        msgFilter,
        `upstream/${base}..HEAD`,
      ],
      {
        cwd: repoDir,
        env: { ...process.env, FILTER_BRANCH_SQUELCH_WARNING: "1" },
        stdio: "pipe",
      }
    );
  }

  /** Creates a named branch from sourceRef and pushes it to the fork. */
  pushBranch(
    repoDir: string,
    forkFullName: string,
    branch: string,
    sourceRef: string
  ): void {
    this.pushBranchInner("git", repoDir, forkFullName, branch, sourceRef);
  }

  pushBranchInner(
    gitBin: string,
    repoDir: string,
    forkFullName: string,
    branch: string,
    sourceRef: string
  ): void {
    const qualifiedRef = `refs/heads/${sourceRef}`;
    this.runCapture(repoDir, gitBin, [
      "checkout",
      "-B",
      branch,
      qualifiedRef,
    ]);

    const forkURL = `https://github.com/${forkFullName}.git`;
    try {
      this.runCapture(repoDir, gitBin, ["remote", "add", "fork", forkURL]);
    } catch {
      this.runCapture(repoDir, gitBin, [
        "remote",
        "set-url",
        "fork",
        forkURL,
      ]);
    }

    // Configure credential helper via gh
    this.runCapture(repoDir, this.bin, ["auth", "setup-git"]);

    // Push to the fork
    this.runCapture(repoDir, gitBin, ["push", "--force", "fork", branch]);
  }

  /** Opens a draft pull request. */
  createPR(
    upstream: string,
    forkOwner: string,
    branch: string,
    base: string,
    title: string,
    body: string
  ): string {
    const head = `${forkOwner}:${branch}`;
    const out = this.runCapture("", this.bin, [
      "pr",
      "create",
      "--repo",
      upstream,
      "--head",
      head,
      "--base",
      base,
      "--title",
      title,
      "--body",
      body,
      "--draft",
    ]);
    return out.trim();
  }

  /** Posts a comment on a pull request. */
  commentOnPR(prURL: string, body: string): void {
    this.runCapture("", this.bin, ["pr", "comment", prURL, "--body", body]);
  }

  /** Changes the title of a pull request. */
  updatePRTitle(prURL: string, title: string): void {
    this.runCapture("", this.bin, [
      "pr",
      "edit",
      prURL,
      "--title",
      title,
    ]);
  }

  /** Fetches top-level conversation comments on a PR. */
  listIssueComments(repo: string, prNumber: number): Comment[] {
    const apiPath = `/repos/${repo}/issues/${prNumber}/comments`;
    const out = this.runCapture("", this.bin, ["api", "--paginate", apiPath]);
    const comments = parseComments(out);
    for (const c of comments) c.type = "issue";
    return comments;
  }

  /** Fetches line-level review comments on a PR. */
  listReviewComments(repo: string, prNumber: number): Comment[] {
    const apiPath = `/repos/${repo}/pulls/${prNumber}/comments`;
    const out = this.runCapture("", this.bin, ["api", "--paginate", apiPath]);
    const comments = parseComments(out);
    for (const c of comments) c.type = "review";
    return comments;
  }

  /** Checks whether a PR is still open. */
  isPROpen(repo: string, prNumber: number): boolean {
    const apiPath = `/repos/${repo}/pulls/${prNumber}`;
    const out = this.runCapture("", this.bin, [
      "api",
      apiPath,
      "--jq",
      ".state",
    ]);
    return out.trim() === "open";
  }

  /** Retrieves both issue and review comments, sorted by ID. */
  fetchAllComments(repo: string, prNumber: number): Comment[] {
    const issue = this.listIssueComments(repo, prNumber);
    const review = this.listReviewComments(repo, prNumber);
    const all = [...issue, ...review];
    all.sort((a, b) => a.id - b.id);
    return all;
  }

  runCapture(dir: string, name: string, args: string[]): string {
    const opts: { cwd?: string; stdio: ["pipe", "pipe", "pipe"] } = {
      stdio: ["pipe", "pipe", "pipe"],
    };
    if (dir) opts.cwd = dir;

    const result = execFileSync(name, args, opts);
    return result.toString();
  }
}

/**
 * Handles gh api --paginate output, which may concatenate multiple JSON arrays.
 */
export function parseComments(out: string): Comment[] {
  const trimmed = out.trim();
  if (!trimmed || trimmed === "[]") return [];

  // gh api --paginate concatenates JSON arrays, e.g. "[...][...]"
  // Split by finding array boundaries
  const all: Comment[] = [];
  let depth = 0;
  let start = -1;

  for (let i = 0; i < trimmed.length; i++) {
    if (trimmed[i] === "[") {
      if (depth === 0) start = i;
      depth++;
    } else if (trimmed[i] === "]") {
      depth--;
      if (depth === 0 && start !== -1) {
        const chunk = trimmed.slice(start, i + 1);
        const page = JSON.parse(chunk) as Comment[];
        all.push(...page);
        start = -1;
      }
    }
  }

  return all;
}
