import * as fs from "node:fs";
import * as path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const promptsDir = path.join(__dirname, "..", "prompts");

export const onInit = fs.readFileSync(
  path.join(promptsDir, "on_init.md"),
  "utf-8"
);
export const onPRComment = fs.readFileSync(
  path.join(promptsDir, "on_pr_comment.md"),
  "utf-8"
);
