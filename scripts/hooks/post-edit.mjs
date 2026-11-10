#!/usr/bin/env node
// Claude-format PostToolUse hook: format the file the agent just edited. Never blocks (always exits 0).
import { execFileSync } from "node:child_process";
import fs from "node:fs";

let input = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (c) => (input += c));
process.stdin.on("end", () => {
  try {
    const file = JSON.parse(input)?.tool_input?.file_path;
    if (!file || !fs.existsSync(file)) return;
    if (file.endsWith(".go")) execFileSync("gofmt", ["-w", "-s", file], { stdio: "ignore" });
    else if (/\.(mjs|js|json)$/.test(file)) execFileSync("npx", ["--no-install", "biome", "format", "--write", file], { stdio: "ignore" });
  } catch {
    // formatting is best effort; the CI gates are the authority
  }
});
