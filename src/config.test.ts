import { describe, it, expect } from "vitest";
import * as fs from "node:fs";
import * as path from "node:path";
import * as os from "node:os";
import { load, repoAllowed, type Config } from "./config.js";

function withConfig(content: string): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "config-test-"));
  const p = path.join(dir, "config.yaml");
  fs.writeFileSync(p, content);
  return p;
}

describe("load", () => {
  it("full config", () => {
    const p = withConfig(
      "slack_webhook: https://hooks.slack.com/test\noutput_dir: /tmp/tasks\ngjoll_env: /path/to/sandbox.tf\n"
    );
    const cfg = load(p);
    expect(cfg.slack_webhook).toBe("https://hooks.slack.com/test");
    expect(cfg.output_dir).toBe("/tmp/tasks");
    expect(cfg.gjoll_env).toBe("/path/to/sandbox.tf");
  });

  it("defaults applied", () => {
    const p = withConfig("slack_webhook: https://hooks.slack.com/test\n");
    const cfg = load(p);
    expect(cfg.output_dir).toBe("./tasks");
    expect(cfg.gjoll_env).toBe("./configs/sandbox.tf");
  });

  it("empty file uses all defaults", () => {
    const p = withConfig("");
    const cfg = load(p);
    expect(cfg.output_dir).toBe("./tasks");
    expect(cfg.gjoll_env).toBe("./configs/sandbox.tf");
    expect(cfg.slack_webhook).toBeUndefined();
  });

  it("allowed_repos parsed", () => {
    const p = withConfig(
      "allowed_repos:\n  - osbuild/osbuild\n  - drellabot/*\n"
    );
    const cfg = load(p);
    expect(cfg.allowed_repos).toEqual(["osbuild/osbuild", "drellabot/*"]);
  });

  it("daemon config parsed", () => {
    const p = withConfig(
      'daemon:\n  poll_interval: "30s"\n  allowed_commenters:\n    - alice\n    - bob\n'
    );
    const cfg = load(p);
    expect(cfg.daemon.poll_interval).toBe("30s");
    expect(cfg.daemon.allowed_commenters).toEqual(["alice", "bob"]);
  });

  it("invalid yaml throws", () => {
    const p = withConfig("{{invalid");
    expect(() => load(p)).toThrow();
  });

  it("missing file throws", () => {
    expect(() => load("/nonexistent/config.yaml")).toThrow();
  });
});

describe("repoAllowed", () => {
  it("exact match", () => {
    const cfg = { allowed_repos: ["osbuild/osbuild"] } as Config;
    expect(repoAllowed(cfg, "osbuild/osbuild")).toBe(true);
  });

  it("wildcard match", () => {
    const cfg = { allowed_repos: ["drellabot/*"] } as Config;
    expect(repoAllowed(cfg, "drellabot/orchestrator")).toBe(true);
  });

  it("no match", () => {
    const cfg = { allowed_repos: ["osbuild/osbuild"] } as Config;
    expect(repoAllowed(cfg, "evil/repo")).toBe(false);
  });

  it("empty list denies all", () => {
    const cfg = { allowed_repos: [] } as Config;
    expect(repoAllowed(cfg, "osbuild/osbuild")).toBe(false);
  });
});
