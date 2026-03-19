import * as http from "node:http";

export type LogLevel = "debug" | "info" | "warn" | "error";

const LEVEL_ORDER: Record<LogLevel, number> = {
  debug: 0,
  info: 1,
  warn: 2,
  error: 3,
};

export interface Logger {
  debug(msg: string, attrs?: Record<string, unknown>): void;
  info(msg: string, attrs?: Record<string, unknown>): void;
  warn(msg: string, attrs?: Record<string, unknown>): void;
  error(msg: string, attrs?: Record<string, unknown>): void;
}

function formatAttrs(attrs?: Record<string, unknown>): string {
  if (!attrs) return "";
  return Object.entries(attrs)
    .map(([k, v]) => ` ${k}=${v}`)
    .join("");
}

function formatTimestamp(): string {
  return new Date().toISOString();
}

class StderrLogger implements Logger {
  private minLevel: LogLevel;

  constructor(minLevel: LogLevel) {
    this.minLevel = minLevel;
  }

  private log(level: LogLevel, msg: string, attrs?: Record<string, unknown>) {
    if (LEVEL_ORDER[level] < LEVEL_ORDER[this.minLevel]) return;
    const ts = formatTimestamp();
    const attrStr = formatAttrs(attrs);
    process.stderr.write(
      `${ts} ${level.toUpperCase()} ${msg}${attrStr}\n`
    );
  }

  debug(msg: string, attrs?: Record<string, unknown>) {
    this.log("debug", msg, attrs);
  }
  info(msg: string, attrs?: Record<string, unknown>) {
    this.log("info", msg, attrs);
  }
  warn(msg: string, attrs?: Record<string, unknown>) {
    this.log("warn", msg, attrs);
  }
  error(msg: string, attrs?: Record<string, unknown>) {
    this.log("error", msg, attrs);
  }
}

/** Posts a message to a Slack webhook URL. */
export function postToSlack(webhookURL: string, text: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const payload = JSON.stringify({ text });
    const url = new URL(webhookURL);

    const req = http.request(
      {
        hostname: url.hostname,
        port: url.port,
        path: url.pathname,
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Content-Length": Buffer.byteLength(payload),
        },
      },
      (res) => {
        if (res.statusCode !== 200) {
          reject(new Error(`slack returned status ${res.statusCode}`));
        } else {
          resolve();
        }
        res.resume();
      }
    );
    req.on("error", reject);
    req.write(payload);
    req.end();
  });
}

class SlackLogger implements Logger {
  private webhookURL: string;

  constructor(webhookURL: string) {
    this.webhookURL = webhookURL;
  }

  private log(level: LogLevel, msg: string, attrs?: Record<string, unknown>) {
    if (LEVEL_ORDER[level] < LEVEL_ORDER.info) return;
    const attrStr = attrs
      ? Object.entries(attrs)
          .map(([k, v]) => ` | ${k}=${v}`)
          .join("")
      : "";
    const text = `*[${level.toUpperCase()}]* ${msg}${attrStr}`;
    // Fire and forget
    postToSlack(this.webhookURL, text).catch((e) => {
      process.stderr.write(`logging: slack error: ${e}\n`);
    });
  }

  debug(msg: string, attrs?: Record<string, unknown>) {
    this.log("debug", msg, attrs);
  }
  info(msg: string, attrs?: Record<string, unknown>) {
    this.log("info", msg, attrs);
  }
  warn(msg: string, attrs?: Record<string, unknown>) {
    this.log("warn", msg, attrs);
  }
  error(msg: string, attrs?: Record<string, unknown>) {
    this.log("error", msg, attrs);
  }
}

class MultiLogger implements Logger {
  private loggers: Logger[];

  constructor(loggers: Logger[]) {
    this.loggers = loggers;
  }

  debug(msg: string, attrs?: Record<string, unknown>) {
    for (const l of this.loggers) l.debug(msg, attrs);
  }
  info(msg: string, attrs?: Record<string, unknown>) {
    for (const l of this.loggers) l.info(msg, attrs);
  }
  warn(msg: string, attrs?: Record<string, unknown>) {
    for (const l of this.loggers) l.warn(msg, attrs);
  }
  error(msg: string, attrs?: Record<string, unknown>) {
    for (const l of this.loggers) l.error(msg, attrs);
  }
}

let defaultLogger: Logger = new StderrLogger("info");

export function getLogger(): Logger {
  return defaultLogger;
}

export function setupLogger(
  slackWebhookURL?: string,
  verbose?: boolean
): Logger {
  const stderrLevel: LogLevel = verbose ? "debug" : "info";
  const stderr = new StderrLogger(stderrLevel);

  if (slackWebhookURL) {
    defaultLogger = new MultiLogger([stderr, new SlackLogger(slackWebhookURL)]);
  } else {
    defaultLogger = stderr;
  }
  return defaultLogger;
}
