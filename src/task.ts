import * as fs from "node:fs";
import * as path from "node:path";

export interface PR {
  url: string;
  repo: string;
  branch: string;
  base: string;
  number?: number;
  last_comment_id?: number;
  closed?: boolean;
}

export interface GitHubResources {
  prs: PR[];
}

export interface Resources {
  github: GitHubResources;
}

export interface State {
  name: string;
  description: string;
  created_at: string;
  updated_at: string;
  author?: string;
  resources: Resources;
}

/**
 * Extracts the pull request number from a GitHub PR URL
 * of the form https://github.com/owner/repo/pull/42.
 */
export function prNumberFromURL(url: string): number {
  const prefix = "/pull/";
  const idx = url.lastIndexOf(prefix);
  if (idx === -1) {
    throw new Error(`URL does not contain /pull/: ${url}`);
  }
  let numStr = url.slice(idx + prefix.length);
  const slashIdx = numStr.indexOf("/");
  if (slashIdx !== -1) {
    numStr = numStr.slice(0, slashIdx);
  }
  const n = parseInt(numStr, 10);
  if (isNaN(n)) {
    throw new Error(`invalid PR number in URL ${url}`);
  }
  return n;
}

/** Dir represents a per-task directory structure. */
export class Dir {
  private root: string;

  constructor(root: string) {
    this.root = root;
  }

  repoPath(): string {
    return path.join(this.root, "repo");
  }

  conversationsPath(): string {
    return path.join(this.root, "conversations");
  }

  transcriptPath(): string {
    return path.join(this.root, "transcript.jsonl");
  }

  private statePath(): string {
    return path.join(this.root, "state.json");
  }

  /** Reads the task state from disk. Returns a default State if the file does not exist. */
  loadState(): State {
    const sp = this.statePath();
    try {
      const data = fs.readFileSync(sp, "utf-8");
      return JSON.parse(data) as State;
    } catch (e: unknown) {
      if ((e as NodeJS.ErrnoException).code === "ENOENT") {
        return {
          name: "",
          description: "",
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
          resources: { github: { prs: [] } },
        };
      }
      throw e;
    }
  }

  private saveState(s: State): void {
    fs.writeFileSync(this.statePath(), JSON.stringify(s, null, 2));
  }

  /** Writes task metadata to state.json. */
  saveMetadata(
    name: string,
    description: string,
    author: string,
    createdAt: Date
  ): void {
    const s = this.loadState();
    s.name = name;
    s.description = description;
    if (author) {
      s.author = author;
    } else {
      delete s.author;
    }
    s.created_at = createdAt.toISOString();
    s.updated_at = createdAt.toISOString();
    this.saveState(s);
  }

  /** Sets updated_at to the given time and persists it. */
  touchUpdatedAt(t: Date): void {
    const s = this.loadState();
    s.updated_at = t.toISOString();
    this.saveState(s);
  }

  /** Appends a PR to the task state and persists it to disk. */
  addPR(pr: PR): void {
    if (!pr.number && pr.url) {
      try {
        pr.number = prNumberFromURL(pr.url);
      } catch {
        // ignore
      }
    }
    const s = this.loadState();
    s.resources.github.prs.push(pr);
    this.saveState(s);
  }

  /** Finds the PR with the given URL and applies the mutation function. */
  updatePR(prURL: string, fn: (pr: PR) => void): void {
    const s = this.loadState();
    const pr = s.resources.github.prs.find((p) => p.url === prURL);
    if (!pr) {
      throw new Error(`PR not found: ${prURL}`);
    }
    fn(pr);
    this.saveState(s);
  }
}

/** Creates a new task directory structure under outputDir. */
export function create(outputDir: string, taskName: string): Dir {
  const root = path.join(outputDir, taskName);
  if (fs.existsSync(root)) {
    throw new Error(`task directory already exists: ${root}`);
  }
  for (const sub of ["repo", "conversations"]) {
    fs.mkdirSync(path.join(root, sub), { recursive: true });
  }
  return new Dir(root);
}

/** Opens an existing task directory. */
export function open(outputDir: string, taskName: string): Dir {
  const root = path.join(outputDir, taskName);
  if (!fs.existsSync(root)) {
    throw new Error(`task directory does not exist: ${root}`);
  }
  return new Dir(root);
}

/** Returns the transcript path for a task by name. */
export function transcriptPathFor(outputDir: string, taskName: string): string {
  return path.join(outputDir, taskName, "transcript.jsonl");
}
