import * as fs from "node:fs";
import { execFileSync } from "node:child_process";
import * as github from "./github.js";
import * as prompts from "./prompts.js";
import * as task from "./task.js";
import { getLogger } from "./logging.js";
import type { Comment } from "./github.js";

export interface PRRef {
  taskName: string;
  outputDir: string;
  pr: task.PR;
}

export type ContinueFunc = (taskName: string, prompt: string) => void;

export class Daemon {
  private gh: github.Runner;
  private interval: number; // ms
  private configPath: string;
  private outputDir: string;
  private allowedCommenters: string[];
  continueFunc: ContinueFunc;
  private running = new Map<string, boolean>();

  constructor(
    ghRunner: github.Runner,
    intervalMs: number,
    configPath: string,
    outputDir: string,
    allowedCommenters: string[]
  ) {
    this.gh = ghRunner;
    this.interval = intervalMs;
    this.configPath = configPath;
    this.outputDir = outputDir;
    this.allowedCommenters = allowedCommenters;
    this.continueFunc = (taskName: string, prompt: string) =>
      this.defaultContinueFunc(taskName, prompt);
  }

  /** Main polling loop. */
  async run(signal: AbortSignal): Promise<void> {
    const log = getLogger();
    while (!signal.aborted) {
      const refs = discoverPRs(this.outputDir);
      if (refs.length === 0) {
        log.info("No open PRs found, waiting before re-discovery", {
          interval: this.interval,
        });
        await sleep(this.interval, signal);
        continue;
      }

      log.info("Discovered PRs", { count: refs.length });

      let perPR = Math.floor(this.interval / refs.length);
      const minInterval = 5000;
      if (perPR < minInterval) perPR = minInterval;

      for (const ref of refs) {
        if (signal.aborted) return;
        this.processPR(ref);
        await sleep(perPR, signal);
      }
    }
  }

  processPR(ref: PRRef): void {
    const log = getLogger();

    if (!ref.pr.number) {
      log.warn("PR has no number, skipping", {
        task: ref.taskName,
        pr: ref.pr.url,
      });
      return;
    }

    // Check if PR is still open
    let open: boolean;
    try {
      open = this.gh.isPROpen(ref.pr.repo, ref.pr.number);
    } catch (e) {
      log.warn("Failed to check PR state", {
        task: ref.taskName,
        error: String(e),
      });
      return;
    }

    if (!open) {
      log.info("PR is closed, marking as closed", {
        task: ref.taskName,
        pr: ref.pr.url,
      });
      try {
        const td = task.open(ref.outputDir, ref.taskName);
        td.updatePR(ref.pr.url, (pr) => {
          pr.closed = true;
        });
      } catch (e) {
        log.warn("Failed to mark PR as closed", { error: String(e) });
      }
      return;
    }

    // Check if task is already running
    if (this.running.get(ref.taskName)) {
      log.debug("Task already running, skipping", { task: ref.taskName });
      return;
    }

    // Fetch comments
    let comments: Comment[];
    try {
      comments = this.gh.fetchAllComments(ref.pr.repo, ref.pr.number);
    } catch (e) {
      log.warn("Failed to fetch comments", {
        task: ref.taskName,
        error: String(e),
      });
      return;
    }

    // Filter new comments
    const newComments = filterNewComments(
      comments,
      ref.pr.last_comment_id || 0,
      this.allowedCommenters
    );
    if (newComments.length === 0) {
      log.debug("No new comments", { task: ref.taskName });
      return;
    }

    log.info("Found new comments", {
      task: ref.taskName,
      count: newComments.length,
    });

    // Update LastCommentID before launching
    const maxID = newComments[newComments.length - 1].id;
    try {
      const td = task.open(ref.outputDir, ref.taskName);
      td.updatePR(ref.pr.url, (pr) => {
        pr.last_comment_id = maxID;
      });
    } catch (e) {
      log.warn("Failed to update LastCommentID", { error: String(e) });
      return;
    }

    const prompt = formatCommentsAsPrompt(newComments);

    // Re-check running state
    if (this.running.get(ref.taskName)) {
      log.debug("Task became running during processing, skipping", {
        task: ref.taskName,
      });
      return;
    }

    this.running.set(ref.taskName, true);

    // Run continue in background
    try {
      this.continueFunc(ref.taskName, prompt);
    } catch (e) {
      log.error("task continue failed", {
        task: ref.taskName,
        error: String(e),
      });
    } finally {
      this.running.delete(ref.taskName);
    }
  }

  private defaultContinueFunc(taskName: string, prompt: string): void {
    const exe = process.argv[1];
    const args = [
      "task",
      "continue",
      "--config",
      this.configPath,
      taskName,
      prompt,
    ];
    execFileSync(process.execPath, [exe, ...args], {
      stdio: "inherit",
    });
  }

  isTaskRunning(taskName: string): boolean {
    return this.running.get(taskName) || false;
  }

  setTaskRunning(taskName: string, running: boolean): void {
    if (running) {
      this.running.set(taskName, true);
    } else {
      this.running.delete(taskName);
    }
  }
}

/** Scans all task directories for state.json files with open PRs. */
export function discoverPRs(outputDir: string): PRRef[] {
  let entries: fs.Dirent[];
  try {
    entries = fs.readdirSync(outputDir, { withFileTypes: true });
  } catch {
    return [];
  }

  const refs: PRRef[] = [];
  for (const entry of entries) {
    if (!entry.isDirectory()) continue;
    const taskName = entry.name;
    let td: task.Dir;
    try {
      td = task.open(outputDir, taskName);
    } catch {
      continue;
    }
    let state: task.State;
    try {
      state = td.loadState();
    } catch {
      continue;
    }
    for (const pr of state.resources.github.prs) {
      if (pr.closed) continue;
      const prCopy = { ...pr };
      if (!prCopy.number && prCopy.url) {
        try {
          prCopy.number = task.prNumberFromURL(prCopy.url);
        } catch {
          // ignore
        }
      }
      refs.push({ taskName, outputDir, pr: prCopy });
    }
  }
  return refs;
}

/** Returns comments with ID > lastCommentID from allowed users. */
export function filterNewComments(
  comments: Comment[],
  lastCommentID: number,
  allowedCommenters: string[]
): Comment[] {
  const allowed = new Set(allowedCommenters);
  return comments.filter(
    (c) => c.id > lastCommentID && allowed.has(c.user.login)
  );
}

/** Formats comments as a chronological prompt. */
export function formatCommentsAsPrompt(comments: Comment[]): string {
  const sorted = [...comments].sort((a, b) => a.id - b.id);
  let out = prompts.onPRComment + "\n";
  for (let i = 0; i < sorted.length; i++) {
    const c = sorted[i];
    if (i > 0) out += "\n---\n\n";
    let header = `@${c.user.login} at ${c.created_at}`;
    if (c.type === "review" && c.path) {
      header += ` on ${c.path}`;
    }
    out += `${header}:\n\n${c.body}\n`;
  }
  return out;
}

/** Polls all open PRs for a task for new comments. */
export function watchTask(
  ghRunner: github.Runner,
  outputDir: string,
  taskName: string,
  allowedCommenters: string[],
  pollIntervalMs: number,
  signal: AbortSignal
): Promise<string> {
  const log = getLogger();
  const td = task.open(outputDir, taskName);
  const state = td.loadState();

  interface WatchPR {
    repo: string;
    number: number;
    baseline: number;
  }

  const prs: WatchPR[] = [];
  for (const pr of state.resources.github.prs) {
    if (pr.closed) continue;
    let num = pr.number;
    if (!num && pr.url) {
      try {
        num = task.prNumberFromURL(pr.url);
      } catch {
        continue;
      }
    }
    if (!num) continue;
    prs.push({
      repo: pr.repo,
      number: num,
      baseline: pr.last_comment_id || 0,
    });
  }

  if (prs.length === 0) {
    throw new Error(`no open PRs found for task ${taskName}`);
  }

  log.info("Watching PRs", { task: taskName, count: prs.length });

  return new Promise(async (resolve, reject) => {
    while (!signal.aborted) {
      for (const pr of prs) {
        const comments = ghRunner.fetchAllComments(pr.repo, pr.number);
        const newComments = filterNewComments(
          comments,
          pr.baseline,
          allowedCommenters
        );
        if (newComments.length > 0) {
          resolve(formatCommentsAsPrompt(newComments));
          return;
        }
      }
      await sleep(pollIntervalMs, signal);
    }
    reject(new Error("aborted"));
  });
}

/** Returns names of all task directories. */
export function listTaskDirs(outputDir: string): string[] {
  try {
    return fs
      .readdirSync(outputDir, { withFileTypes: true })
      .filter((e) => e.isDirectory())
      .map((e) => e.name);
  } catch {
    return [];
  }
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal?.aborted) {
      resolve();
      return;
    }
    const timer = setTimeout(resolve, ms);
    signal?.addEventListener("abort", () => {
      clearTimeout(timer);
      resolve();
    });
  });
}
