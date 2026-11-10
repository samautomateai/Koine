#!/usr/bin/env node
// Line-length and file-length gate for Go, JS and TS sources.
//   node scripts/sizecheck.mjs [--max-line 160] [--max-file 600] [--tab 4] [paths...]
// Tabs count as --tab columns. Generated/vendored paths are skipped. Exit 1 on any violation.
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

const SKIP_DIRS = new Set(["node_modules", ".git", "bin", "vendor", "testdata", "fixture", "wailsjs", "dist", "build"]);
const EXT = /\.(go|mjs|js|ts)$/;
// Data and generated files that are long by nature.
const SKIP_FILES = [/\.pb\.go$/, /_generated\.go$/, /models_catalog/];

export function* walk(dir) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    if (SKIP_DIRS.has(e.name)) continue;
    const p = path.join(dir, e.name);
    if (e.isDirectory()) yield* walk(p);
    else if (EXT.test(e.name) && !SKIP_FILES.some((r) => r.test(p))) yield p;
  }
}

export function check(text, { maxLine, maxFile, tab }) {
  const lines = text.split("\n");
  if (lines.at(-1) === "") lines.pop();
  const problems = [];
  if (lines.length > maxFile) problems.push({ line: 0, msg: `file has ${lines.length} lines (max ${maxFile})` });
  lines.forEach((l, i) => {
    const width = l.replaceAll("\t", " ".repeat(tab)).length;
    if (width > maxLine) problems.push({ line: i + 1, msg: `line is ${width} columns (max ${maxLine})` });
  });
  return problems;
}

export function main(argv) {
  const num = (k, d) => {
    const i = argv.indexOf(k);
    return i >= 0 ? Number(argv[i + 1]) : d;
  };
  const opts = { maxLine: num("--max-line", 160), maxFile: num("--max-file", 600), tab: num("--tab", 4) };
  const skip = new Set(["--max-line", "--max-file", "--tab"]);
  const roots = argv.filter((a, i) => !skip.has(a) && !skip.has(argv[i - 1]));
  let bad = 0;
  for (const root of roots.length ? roots : ["."]) {
    const files = fs.statSync(root).isDirectory() ? [...walk(root)] : [root];
    for (const f of files) {
      for (const p of check(fs.readFileSync(f, "utf8"), opts)) {
        console.log(`${f}${p.line ? ":" + p.line : ""}: ${p.msg}`);
        bad++;
      }
    }
  }
  if (bad) console.error(`${bad} size violation(s)`);
  return bad ? 1 : 0;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) process.exit(main(process.argv.slice(2)));
