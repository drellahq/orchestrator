import type { Writable } from "node:stream";

/** Returns the first line of s, truncated to max characters. */
export function firstLine(s: string, max: number): string {
  const nlIdx = s.indexOf("\n");
  if (nlIdx >= 0) {
    s = s.slice(0, nlIdx);
  }
  if (s.length > max) {
    return s.slice(0, max) + "…";
  }
  return s;
}

/** Extracts a short description from a tool's input. */
export function toolInputSummary(
  name: string,
  raw: Record<string, unknown> | undefined
): string {
  if (!raw) return "";

  switch (name) {
    case "Write":
    case "Read":
    case "Edit":
      if (typeof raw.file_path === "string") return raw.file_path;
      break;
    case "Bash":
      if (typeof raw.description === "string") return raw.description;
      if (typeof raw.command === "string") return firstLine(raw.command, 80);
      break;
    case "Grep":
    case "Glob":
      if (typeof raw.pattern === "string") return raw.pattern;
      break;
  }

  // Fallback: try common field names
  for (const key of ["path", "query", "url", "name"]) {
    if (typeof raw[key] === "string") return firstLine(raw[key] as string, 80);
  }
  return "";
}

/** Formats a single stream-json line for human readability. */
export function formatTranscriptLine(
  line: string,
  verbose: boolean
): string {
  let msg: {
    type?: string;
    subtype?: string;
    message?: {
      content?: Array<{
        type?: string;
        text?: string;
        name?: string;
        input?: Record<string, unknown>;
        thinking?: string;
        content?: unknown;
      }>;
    };
    duration_ms?: number;
    num_turns?: number;
    total_cost_usd?: number;
  };

  try {
    msg = JSON.parse(line);
  } catch {
    return "";
  }

  let out = "";
  switch (msg.type) {
    case "assistant":
      for (const c of msg.message?.content || []) {
        switch (c.type) {
          case "text":
            out += (c.text || "") + "\n";
            break;
          case "tool_use": {
            const summary = toolInputSummary(c.name || "", c.input);
            if (summary) {
              out += `[tool] ${c.name}: ${summary}\n`;
            } else {
              out += `[tool] ${c.name}\n`;
            }
            break;
          }
          case "thinking":
            if (verbose && c.thinking) {
              out += `[thinking] ${c.thinking}\n`;
            }
            break;
        }
      }
      break;
    case "user":
      for (const c of msg.message?.content || []) {
        if (c.type !== "tool_result" || !c.content) continue;
        if (typeof c.content === "string" && c.content) {
          out += `  → ${firstLine(c.content, 200)}\n`;
        }
      }
      break;
    case "result": {
      const subtype = msg.subtype || "done";
      const duration = (msg.duration_ms || 0) / 1000;
      if (msg.total_cost_usd && msg.total_cost_usd > 0) {
        out = `[result] ${subtype} (${msg.num_turns} turns, ${duration.toFixed(1)}s, $${msg.total_cost_usd!.toFixed(2)})\n`;
      } else if (msg.duration_ms && msg.duration_ms > 0) {
        out = `[result] ${subtype} (${msg.num_turns} turns, ${duration.toFixed(1)}s)\n`;
      } else {
        out = `[result] ${subtype}\n`;
      }
      break;
    }
  }
  return out;
}

/**
 * TranscriptWriter buffers input until complete JSONL lines are available,
 * then formats each line for human readability.
 */
export class TranscriptWriter {
  private w: Writable;
  private buf = "";
  private verbose: boolean;

  constructor(w: Writable, verbose: boolean) {
    this.w = w;
    this.verbose = verbose;
  }

  write(data: Buffer | string): void {
    this.buf += data.toString();
    let idx: number;
    while ((idx = this.buf.indexOf("\n")) >= 0) {
      const line = this.buf.slice(0, idx);
      this.buf = this.buf.slice(idx + 1);
      const formatted = formatTranscriptLine(line, this.verbose);
      if (formatted) {
        this.w.write(formatted);
      }
    }
  }
}
