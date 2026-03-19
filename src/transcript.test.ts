import { describe, it, expect } from "vitest";
import {
  firstLine,
  toolInputSummary,
  formatTranscriptLine,
  TranscriptWriter,
} from "./transcript.js";
import { Writable } from "node:stream";

describe("firstLine", () => {
  it("returns full string when short and no newline", () => {
    expect(firstLine("hello world", 80)).toBe("hello world");
  });

  it("truncates at newline", () => {
    expect(firstLine("first\nsecond", 80)).toBe("first");
  });

  it("truncates at max length", () => {
    expect(firstLine("abcdefghij", 5)).toBe("abcde…");
  });

  it("truncates at newline before max", () => {
    expect(firstLine("abc\ndefghij", 5)).toBe("abc");
  });
});

describe("toolInputSummary", () => {
  it("returns file_path for Write", () => {
    expect(toolInputSummary("Write", { file_path: "/foo/bar.ts" })).toBe(
      "/foo/bar.ts"
    );
  });

  it("returns file_path for Read", () => {
    expect(toolInputSummary("Read", { file_path: "/a/b.ts" })).toBe("/a/b.ts");
  });

  it("returns description for Bash", () => {
    expect(toolInputSummary("Bash", { description: "run tests" })).toBe(
      "run tests"
    );
  });

  it("returns command for Bash when no description", () => {
    expect(toolInputSummary("Bash", { command: "npm test" })).toBe("npm test");
  });

  it("returns pattern for Grep", () => {
    expect(toolInputSummary("Grep", { pattern: "TODO" })).toBe("TODO");
  });

  it("falls back to common fields", () => {
    expect(toolInputSummary("Unknown", { path: "/some/path" })).toBe(
      "/some/path"
    );
    expect(toolInputSummary("Unknown", { query: "search term" })).toBe(
      "search term"
    );
  });

  it("returns empty for undefined input", () => {
    expect(toolInputSummary("Write", undefined)).toBe("");
  });

  it("returns empty when no matching fields", () => {
    expect(toolInputSummary("Unknown", { foo: 42 })).toBe("");
  });
});

describe("formatTranscriptLine", () => {
  it("formats assistant text", () => {
    const line = JSON.stringify({
      type: "assistant",
      message: {
        content: [{ type: "text", text: "Hello world" }],
      },
    });
    expect(formatTranscriptLine(line, false)).toBe("Hello world\n");
  });

  it("formats tool use", () => {
    const line = JSON.stringify({
      type: "assistant",
      message: {
        content: [
          {
            type: "tool_use",
            name: "Read",
            input: { file_path: "/foo.ts" },
          },
        ],
      },
    });
    expect(formatTranscriptLine(line, false)).toBe("[tool] Read: /foo.ts\n");
  });

  it("formats tool use without summary", () => {
    const line = JSON.stringify({
      type: "assistant",
      message: {
        content: [{ type: "tool_use", name: "CustomTool", input: {} }],
      },
    });
    expect(formatTranscriptLine(line, false)).toBe("[tool] CustomTool\n");
  });

  it("hides thinking when not verbose", () => {
    const line = JSON.stringify({
      type: "assistant",
      message: {
        content: [{ type: "thinking", thinking: "deep thoughts" }],
      },
    });
    expect(formatTranscriptLine(line, false)).toBe("");
  });

  it("shows thinking when verbose", () => {
    const line = JSON.stringify({
      type: "assistant",
      message: {
        content: [{ type: "thinking", thinking: "deep thoughts" }],
      },
    });
    expect(formatTranscriptLine(line, true)).toBe(
      "[thinking] deep thoughts\n"
    );
  });

  it("formats result with cost", () => {
    const line = JSON.stringify({
      type: "result",
      subtype: "success",
      duration_ms: 5000,
      num_turns: 3,
      total_cost_usd: 1.23,
    });
    expect(formatTranscriptLine(line, false)).toBe(
      "[result] success (3 turns, 5.0s, $1.23)\n"
    );
  });

  it("formats result without cost", () => {
    const line = JSON.stringify({
      type: "result",
      subtype: "success",
      duration_ms: 5000,
      num_turns: 3,
    });
    expect(formatTranscriptLine(line, false)).toBe(
      "[result] success (3 turns, 5.0s)\n"
    );
  });

  it("formats result without duration", () => {
    const line = JSON.stringify({
      type: "result",
      subtype: "done",
    });
    expect(formatTranscriptLine(line, false)).toBe("[result] done\n");
  });

  it("formats tool_result in user message", () => {
    const line = JSON.stringify({
      type: "user",
      message: {
        content: [
          { type: "tool_result", content: "file contents here" },
        ],
      },
    });
    expect(formatTranscriptLine(line, false)).toContain(
      "→ file contents here"
    );
  });

  it("returns empty for invalid JSON", () => {
    expect(formatTranscriptLine("not json", false)).toBe("");
  });

  it("returns empty for unknown type", () => {
    expect(formatTranscriptLine(JSON.stringify({ type: "unknown" }), false)).toBe("");
  });
});

describe("TranscriptWriter", () => {
  it("buffers incomplete lines", () => {
    const chunks: string[] = [];
    const w = new Writable({
      write(chunk, _enc, cb) {
        chunks.push(chunk.toString());
        cb();
      },
    });

    const tw = new TranscriptWriter(w, false);
    const line = JSON.stringify({
      type: "assistant",
      message: { content: [{ type: "text", text: "hi" }] },
    });

    // Send partial line
    tw.write(line.slice(0, 10));
    expect(chunks).toHaveLength(0);

    // Complete the line
    tw.write(line.slice(10) + "\n");
    expect(chunks).toHaveLength(1);
    expect(chunks[0]).toBe("hi\n");
  });

  it("handles multiple lines in one write", () => {
    const chunks: string[] = [];
    const w = new Writable({
      write(chunk, _enc, cb) {
        chunks.push(chunk.toString());
        cb();
      },
    });

    const tw = new TranscriptWriter(w, false);
    const line1 = JSON.stringify({
      type: "assistant",
      message: { content: [{ type: "text", text: "first" }] },
    });
    const line2 = JSON.stringify({
      type: "assistant",
      message: { content: [{ type: "text", text: "second" }] },
    });

    tw.write(line1 + "\n" + line2 + "\n");
    expect(chunks).toHaveLength(2);
    expect(chunks[0]).toBe("first\n");
    expect(chunks[1]).toBe("second\n");
  });
});
