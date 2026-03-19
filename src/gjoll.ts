import { execFileSync, execSync, type SpawnSyncOptions } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";
import type { Writable } from "node:stream";
import { spawn } from "node:child_process";

export interface SSHOpts {
  proxy?: boolean;
  reverseTunnels?: string[];
}

function sshOptsArgs(opts?: SSHOpts): string[] {
  const a: string[] = [];
  if (!opts) return a;
  if (opts.proxy) a.push("--proxy");
  for (const rt of opts.reverseTunnels || []) {
    a.push("-R", rt);
  }
  return a;
}

/** Runner wraps gjoll CLI commands. */
export class Runner {
  private bin: string;

  constructor(bin?: string) {
    this.bin = bin || "gjoll";
  }

  /** Provisions a sandbox VM from a .tf environment. */
  up(name: string, tfPath: string): void {
    this.run(["up", "-n", name, tfPath]);
  }

  /** Starts a stopped sandbox VM. */
  start(name: string): void {
    this.run(["start", name]);
  }

  /** Runs a command inside the sandbox (no proxy tunnels). */
  ssh(name: string, command: string): void {
    this.run(["ssh", name, "--", command]);
  }

  /** Runs a command inside the sandbox with the given SSH options. */
  sshProxy(name: string, opts: SSHOpts | undefined, command: string): void {
    const args = ["ssh", ...sshOptsArgs(opts), name, "--", command];
    this.runInteractive(args);
  }

  /** Fetches committed code from the sandbox into a local git repo. */
  pull(name: string, remotePath: string, localRepoDir: string): void {
    fs.mkdirSync(localRepoDir, { recursive: true });
    const gitDir = path.join(localRepoDir, ".git");
    if (!fs.existsSync(gitDir)) {
      this.runInDir(localRepoDir, "git", ["init"]);
    }
    this.runInDir(localRepoDir, this.bin, [
      "pull",
      name,
      "--path",
      remotePath,
    ]);
  }

  /** Copies files to/from a sandbox. */
  cp(name: string, src: string, dest: string): void {
    this.run(["cp", name, src, dest]);
  }

  /** Stops a running sandbox. */
  stop(name: string): void {
    this.run(["stop", name]);
  }

  /**
   * Runs a command inside the sandbox with the given SSH options,
   * writing the command's stdout to w.
   */
  sshProxyOutput(
    name: string,
    w: Writable,
    opts: SSHOpts | undefined,
    command: string
  ): Promise<void> {
    const args = ["ssh", ...sshOptsArgs(opts), name, "--", command];
    return new Promise((resolve, reject) => {
      const child = spawn(this.bin, args, {
        stdio: ["inherit", "pipe", "inherit"],
      });
      child.stdout!.pipe(w, { end: false });
      child.on("close", (code) => {
        if (code !== 0) {
          reject(new Error(`gjoll ${args[0]}: exit code ${code}`));
        } else {
          resolve();
        }
      });
      child.on("error", reject);
    });
  }

  /** Destroys a sandbox and all its resources. */
  down(name: string): void {
    this.run(["down", name]);
  }

  private run(args: string[]): void {
    execFileSync(this.bin, args, {
      stdio: ["pipe", "inherit", "inherit"],
    });
  }

  private runInteractive(args: string[]): void {
    execFileSync(this.bin, args, {
      stdio: "inherit",
    });
  }

  private runInDir(dir: string, name: string, args: string[]): void {
    execFileSync(name, args, {
      cwd: dir,
      stdio: ["pipe", "inherit", "inherit"],
    });
  }
}
