import { describe, it, expect } from "vitest";
import { parseComments } from "./github.js";

describe("parseComments", () => {
  it("parses empty output", () => {
    expect(parseComments("")).toEqual([]);
    expect(parseComments("[]")).toEqual([]);
  });

  it("parses single array", () => {
    const input = JSON.stringify([
      {
        id: 1,
        body: "hello",
        user: { login: "alice" },
        created_at: "2025-01-01T00:00:00Z",
      },
    ]);
    const comments = parseComments(input);
    expect(comments).toHaveLength(1);
    expect(comments[0].id).toBe(1);
    expect(comments[0].body).toBe("hello");
    expect(comments[0].user.login).toBe("alice");
  });

  it("handles paginated output (concatenated arrays)", () => {
    const page1 = JSON.stringify([
      {
        id: 1,
        body: "first",
        user: { login: "alice" },
        created_at: "2025-01-01T00:00:00Z",
      },
    ]);
    const page2 = JSON.stringify([
      {
        id: 2,
        body: "second",
        user: { login: "bob" },
        created_at: "2025-01-02T00:00:00Z",
      },
    ]);
    const comments = parseComments(page1 + page2);
    expect(comments).toHaveLength(2);
    expect(comments[0].id).toBe(1);
    expect(comments[1].id).toBe(2);
  });

  it("handles whitespace between pages", () => {
    const page1 = JSON.stringify([
      {
        id: 10,
        body: "a",
        user: { login: "u" },
        created_at: "2025-01-01T00:00:00Z",
      },
    ]);
    const page2 = JSON.stringify([
      {
        id: 20,
        body: "b",
        user: { login: "u" },
        created_at: "2025-01-01T00:00:00Z",
      },
    ]);
    const comments = parseComments(page1 + "\n" + page2);
    expect(comments).toHaveLength(2);
  });
});
