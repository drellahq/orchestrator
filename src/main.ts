#!/usr/bin/env node

import { Command } from "commander";
import * as fs from "node:fs";
import * as path from "node:path";
import * as config from "./config.js";
import * as task from "./task.js";
import * as gjollMod from "./gjoll.js";
import * as githubMod from "./github.js";
import * as mcpMod from "./mcp.js";
import * as prompts from "./prompts.js";
import * as daemonMod from "./daemon.js";
import { setupLogger, getLogger } from "./logging.js";
import {
  formatTranscriptLine,
  TranscriptWriter,
} from "./transcript.js";
import { Writable } from "node:stream";
import { tmpdir } from "node:os";

const program = new Command();

program
  .name("orchestrator")
  .description("Orchestrate agentic Claude sandboxes")
  .option("-c, --config <path>", "config file path", "./orchestrator.yaml")
  .option("-v, --verbose", "enable debug logging", false);

function loadConfigAndSetupLogging(opts: Record<string, unknown>): config.Config {
  const configPath = (opts.config as string) || "./orchestrator.yaml";
  const verbose = !!opts.verbose;
  const cfg = config.load(configPath);
  setupLogger(cfg.slack_webhook, verbose);
  return cfg;
}

function logPreflightWarnings(cfg: config.Config): githubMod.Runner {
  const log = getLogger();
  if (cfg.allowed_repos.length === 0) {
    log.warn(
      "allowed_repos is empty; open_pr, update_pr, and comment_on_pr tools will not be available"
    );
  }

  const ghRunner = new githubMod.Runner();
  try {
    ghRunner.authenticatedUser();
  } catch {
    log.warn(
      "GitHub CLI not authenticated; open_pr, update_pr, and comment_on_pr tools will not be available"
    );
  }

  return ghRunner;
}

function buildRunScript(
  taskDescription: string,
  continueSession: boolean
): string {
  const escapedDesc = taskDescription.replace(/'/g, "'\\''");
  const claudeFlags = continueSession ? "--continue" : "";
  const teeFlag = continueSession ? "-a" : "";

  return `#!/bin/bash
source ~/.bashrc
stdbuf -oL claude --dangerously-skip-permissions -p --verbose \\
  --output-format stream-json --append-system-prompt-file ~/system-prompt.md \\
  ${claudeFlags} '${escapedDesc}' \\
  </dev/null | stdbuf -oL tee ${teeFlag} ~/transcript.jsonl
`;
}

function setupSandbox(
  runner: gjollMod.Runner,
  taskName: string
): void {
  // Configure git
  runner.ssh(taskName, "git config --global user.name Drellabot");
  runner.ssh(
    taskName,
    "git config --global user.email imagebuilder-bots+drella@redhat.com"
  );

  // Write system prompt to a temp file and copy it to the sandbox
  const tmpFile = path.join(
    tmpdir(),
    `prompt-${Date.now()}-${Math.random().toString(36).slice(2)}.md`
  );
  fs.writeFileSync(tmpFile, prompts.onInit);
  try {
    runner.cp(taskName, tmpFile, ":~/system-prompt.md");
  } finally {
    fs.unlinkSync(tmpFile);
  }

  // Register MCP server with Claude
  const mcpURL = `http://localhost:${mcpMod.MCP_REMOTE_PORT}/mcp`;
  runner.ssh(
    taskName,
    `claude mcp add --transport http orchestrator ${mcpURL} --scope user`
  );
}

async function executeTask(
  taskName: string,
  taskDescription: string,
  taskDir: task.Dir,
  cfg: config.Config,
  ghRunner: githubMod.Runner,
  continueSession: boolean,
  author: string
): Promise<void> {
  const log = getLogger();
  const runner = new gjollMod.Runner();

  // Start MCP server
  const mcpSrv = new mcpMod.Server(
    log,
    taskName,
    taskDir,
    runner,
    ghRunner,
    cfg.allowed_repos,
    author
  );
  await mcpSrv.start();

  const mcpPort = mcpSrv.port();
  const mcpTunnel = `${mcpMod.MCP_REMOTE_PORT}:localhost:${mcpPort}`;
  log.debug("MCP server started", { task: taskName, port: mcpPort });

  try {
    if (continueSession) {
      log.info("Resuming sandbox", { task: taskName });
      runner.start(taskName);
    } else {
      const tfPath = path.resolve(cfg.gjoll_env);
      log.info("Provisioning sandbox", { task: taskName });
      runner.up(taskName, tfPath);
    }

    log.info("Sandbox provisioned", { task: taskName });

    if (!continueSession) {
      setupSandbox(runner, taskName);
      log.debug("Sandbox setup complete", { task: taskName });
    }

    // Build the Claude run script
    log.info("Running Claude", { task: taskName });
    const runScript = buildRunScript(taskDescription, continueSession);

    const tmpRun = path.join(
      tmpdir(),
      `run-claude-${Date.now()}-${Math.random().toString(36).slice(2)}.sh`
    );
    fs.writeFileSync(tmpRun, runScript);
    try {
      runner.cp(taskName, tmpRun, ":/tmp/run-claude.sh");
    } finally {
      fs.unlinkSync(tmpRun);
    }
    runner.ssh(taskName, "chmod +x /tmp/run-claude.sh");

    const sshOpts: gjollMod.SSHOpts = {
      proxy: true,
      reverseTunnels: [mcpTunnel],
    };

    // Open transcript file for real-time writing
    const transcriptFlags = continueSession ? "a" : "w";
    const transcriptStream = fs.createWriteStream(taskDir.transcriptPath(), {
      flags: transcriptFlags,
    });

    const verbose = program.opts().verbose;
    const tw = new TranscriptWriter(process.stdout as Writable, verbose);

    // Create a writable that tees to both transcript writer and file
    const teeStream = new Writable({
      write(chunk: Buffer, _encoding: string, callback: () => void) {
        tw.write(chunk);
        transcriptStream.write(chunk);
        callback();
      },
    });

    try {
      await runner.sshProxyOutput(
        taskName,
        teeStream,
        sshOpts,
        "/tmp/run-claude.sh"
      );
    } catch (e) {
      log.error("Claude exited with error", {
        task: taskName,
        error: String(e),
      });
    }

    transcriptStream.end();
    log.info("Claude finished", { task: taskName });

    taskDir.touchUpdatedAt(new Date());
    log.info("Task completed", { task: taskName });
  } finally {
    // Cleanup: copy artifacts and stop sandbox
    log.debug("Copying transcript", { task: taskName });
    try {
      runner.cp(taskName, ":~/transcript.jsonl", taskDir.transcriptPath());
    } catch (e) {
      log.warn("Failed to copy transcript", {
        task: taskName,
        error: String(e),
      });
    }

    log.debug("Copying conversations", { task: taskName });
    try {
      runner.cp(taskName, ":~/.claude/", taskDir.conversationsPath());
    } catch (e) {
      log.warn("Failed to copy conversations", {
        task: taskName,
        error: String(e),
      });
    }

    log.debug("Syncing filesystem", { task: taskName });
    try {
      runner.ssh(taskName, "sync");
    } catch {
      // ignore
    }

    log.debug("Stopping sandbox", { task: taskName });
    try {
      runner.stop(taskName);
    } catch (e) {
      log.warn("Failed to stop sandbox", {
        task: taskName,
        error: String(e),
      });
    }

    await mcpSrv.stop();
  }
}

// task command
const taskCmd = program.command("task").description("Manage tasks");

taskCmd
  .command("new")
  .description("Run a new task in a sandboxed Claude instance")
  .argument("<task-name>", "task name")
  .argument("<task-description...>", "task description")
  .option(
    "--author <author>",
    'co-author to add to PR commits (e.g. "Jane Doe <jane@example.com>")'
  )
  .action(
    async (
      taskName: string,
      taskDescription: string[],
      options: { author?: string }
    ) => {
      const opts = program.opts();
      const cfg = loadConfigAndSetupLogging(opts);
      const ghRunner = logPreflightWarnings(cfg);
      const log = getLogger();

      log.info("Task started", { task: taskName });

      const taskDir = task.create(cfg.output_dir, taskName);
      const author = options.author || "";
      taskDir.saveMetadata(
        taskName,
        taskDescription.join(" "),
        author,
        new Date()
      );

      await executeTask(
        taskName,
        taskDescription.join(" "),
        taskDir,
        cfg,
        ghRunner,
        false,
        author
      );
    }
  );

taskCmd
  .command("continue")
  .description("Continue a stopped task with a new prompt")
  .argument("<task-name>", "task name")
  .argument("<task-description...>", "new prompt")
  .action(async (taskName: string, taskDescription: string[]) => {
    const opts = program.opts();
    const cfg = loadConfigAndSetupLogging(opts);
    const ghRunner = logPreflightWarnings(cfg);
    const log = getLogger();

    log.info("Task continuing", { task: taskName });

    const taskDir = task.open(cfg.output_dir, taskName);
    const state = taskDir.loadState();

    await executeTask(
      taskName,
      taskDescription.join(" "),
      taskDir,
      cfg,
      ghRunner,
      true,
      state.author || ""
    );
  });

taskCmd
  .command("watch")
  .description("Poll a task's PRs for new comments (debug tool)")
  .argument("<task-name>", "task name")
  .option("--timeout <duration>", "stop waiting after this duration")
  .action(
    async (taskName: string, options: { timeout?: string }) => {
      const opts = program.opts();
      const cfg = loadConfigAndSetupLogging(opts);
      const ghRunner = new githubMod.Runner();
      const ac = new AbortController();

      process.on("SIGINT", () => ac.abort());
      process.on("SIGTERM", () => ac.abort());

      if (options.timeout) {
        const ms = parseDuration(options.timeout);
        setTimeout(() => ac.abort(), ms);
      }

      const prompt = await daemonMod.watchTask(
        ghRunner,
        cfg.output_dir,
        taskName,
        cfg.daemon.allowed_commenters || [],
        5000,
        ac.signal
      );
      process.stdout.write(prompt);
    }
  );

// log command
program
  .command("log")
  .description("Show Claude transcript for a task")
  .argument("<task-name>", "task name")
  .option("-f, --follow", "follow live transcript via SSH", false)
  .action(
    async (taskName: string, options: { follow: boolean }) => {
      const opts = program.opts();

      if (options.follow) {
        const runner = new gjollMod.Runner();
        const tw = new TranscriptWriter(
          process.stdout as Writable,
          opts.verbose
        );
        const teeStream = new Writable({
          write(
            chunk: Buffer,
            _encoding: string,
            callback: () => void
          ) {
            tw.write(chunk);
            callback();
          },
        });
        const ac = new AbortController();
        process.on("SIGINT", () => ac.abort());
        try {
          await runner.sshProxyOutput(
            taskName,
            teeStream,
            { proxy: true },
            "tail -f ~/transcript.jsonl"
          );
        } catch {
          // Exited (e.g. abort)
        }
        return;
      }

      const cfg = config.load(opts.config);
      const transcriptPath = task.transcriptPathFor(
        cfg.output_dir,
        taskName
      );
      const data = fs.readFileSync(transcriptPath, "utf-8");
      for (const line of data.split("\n")) {
        if (!line.trim()) continue;
        const formatted = formatTranscriptLine(line, opts.verbose);
        if (formatted) process.stdout.write(formatted);
      }
    }
  );

// daemon command
program
  .command("daemon")
  .description(
    "Poll GitHub PRs for new comments and trigger task continue"
  )
  .option(
    "--interval <duration>",
    "poll interval (e.g. 60s, 5m); overrides config"
  )
  .action(async (options: { interval?: string }) => {
    const opts = program.opts();
    const cfg = loadConfigAndSetupLogging(opts);
    const log = getLogger();

    let intervalMs = 60000;
    if (cfg.daemon.poll_interval) {
      intervalMs = parseDuration(cfg.daemon.poll_interval);
    }
    if (options.interval) {
      intervalMs = parseDuration(options.interval);
    }

    const ghRunner = new githubMod.Runner();
    try {
      ghRunner.authenticatedUser();
    } catch {
      log.error("GitHub CLI not authenticated");
      process.exit(1);
    }

    if (!cfg.daemon.allowed_commenters?.length) {
      log.warn(
        "daemon.allowed_commenters is empty; no comments will trigger task continue"
      );
    }

    const d = new daemonMod.Daemon(
      ghRunner,
      intervalMs,
      opts.config,
      cfg.output_dir,
      cfg.daemon.allowed_commenters || []
    );

    const ac = new AbortController();
    process.on("SIGINT", () => ac.abort());
    process.on("SIGTERM", () => ac.abort());

    log.info("Daemon starting", {
      interval: intervalMs,
      output_dir: cfg.output_dir,
      allowed_commenters: cfg.daemon.allowed_commenters,
    });

    await d.run(ac.signal);
  });

/** Parses duration strings like "60s", "5m", "1h" to milliseconds. */
function parseDuration(s: string): number {
  const match = s.match(/^(\d+)(ms|s|m|h)?$/);
  if (!match) throw new Error(`invalid duration: ${s}`);
  const n = parseInt(match[1], 10);
  switch (match[2]) {
    case "ms":
      return n;
    case "s":
    case undefined:
      return n * 1000;
    case "m":
      return n * 60 * 1000;
    case "h":
      return n * 60 * 60 * 1000;
    default:
      throw new Error(`invalid duration unit: ${match[2]}`);
  }
}

program.parse();
