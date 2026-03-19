import * as fs from "node:fs";
import * as yaml from "js-yaml";
import { minimatch } from "minimatch";

export interface DaemonConfig {
  poll_interval?: string;
  allowed_commenters?: string[];
}

export interface Config {
  slack_webhook?: string;
  output_dir: string;
  gjoll_env: string;
  allowed_repos: string[];
  daemon: DaemonConfig;
}

/**
 * Reports whether repo is permitted by the allowed_repos allowlist.
 * Each entry may contain wildcards understood by minimatch (e.g. "org/*").
 * An empty list denies all repos.
 */
export function repoAllowed(cfg: Config, repo: string): boolean {
  return cfg.allowed_repos.some((pattern) => minimatch(repo, pattern));
}

export function load(path: string): Config {
  const data = fs.readFileSync(path, "utf-8");
  const raw = (yaml.load(data) || {}) as Record<string, unknown>;

  return {
    slack_webhook: (raw.slack_webhook as string) || undefined,
    output_dir: (raw.output_dir as string) || "./tasks",
    gjoll_env: (raw.gjoll_env as string) || "./configs/sandbox.tf",
    allowed_repos: (raw.allowed_repos as string[]) || [],
    daemon: {
      poll_interval: (raw.daemon as Record<string, unknown>)?.poll_interval as
        | string
        | undefined,
      allowed_commenters:
        ((raw.daemon as Record<string, unknown>)?.allowed_commenters as
          | string[]
          | undefined) || [],
    },
  };
}
