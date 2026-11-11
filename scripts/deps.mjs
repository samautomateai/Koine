#!/usr/bin/env node
// Finds and updates the versions Dependabot does not manage: the Go toolchain, tools pinned in CI and the Taskfile
// (golangci-lint, govulncheck, Biome), and GitHub Action references. Go modules and npm packages are handled by
// `task deps:update` (go get -u, npm update) and by Dependabot.
//   node scripts/deps.mjs check            print outdated pins; exit 1 if any
//   node scripts/deps.mjs update           rewrite the pinned versions in place
//   --root <dir>                           repository root (default: cwd)
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

/** Tag names of a GitHub repository, via `git ls-remote` (no API token needed). */
export function remoteTags(repo) {
  const out = execFileSync("git", ["ls-remote", "--tags", "--refs", `https://github.com/${repo}.git`], {
    encoding: "utf8",
    timeout: 60000,
  });
  return out
    .split("\n")
    .map((l) => l.split("refs/tags/")[1])
    .filter(Boolean);
}

const cmp = (a, b) => {
  for (let i = 0; i < 3; i++) if ((a[i] ?? 0) !== (b[i] ?? 0)) return (a[i] ?? 0) - (b[i] ?? 0);
  return 0;
};
const nums = (s) => s.split(".").map(Number);

/** Highest version among tags matching `re`, whose first group is the version string. Pre-releases never match. */
export function latest(tags, re) {
  let best = null;
  for (const t of tags) {
    const m = re.exec(t);
    if (m && (!best || cmp(nums(m[1]), nums(best)) > 0)) best = m[1];
  }
  return best;
}

/** Pins to track. Each has the files it lives in, a regex whose group 1 is the current version, and where the latest comes from. */
export function targets() {
  const go = { files: ["go.mod", "desktop/go.mod", "tui-bubbletea/go.mod"], re: /^go (\d+\.\d+(?:\.\d+)?)$/m };
  return [
    {
      name: "Go toolchain",
      repo: "golang/go",
      tag: /^go(\d+\.\d+(?:\.\d+)?)$/,
      normalize: (v) => (v.split(".").length === 2 ? `${v}.0` : v),
      ...go,
    },
    {
      name: "golangci-lint",
      repo: "golangci/golangci-lint",
      tag: /^v(\d+\.\d+\.\d+)$/,
      files: [".github/workflows/ci.yml"],
      re: /GOLANGCI_LINT_VERSION: v(\d+\.\d+\.\d+)/,
    },
    {
      name: "govulncheck",
      repo: "golang/vuln",
      tag: /^v(\d+\.\d+\.\d+)$/,
      files: [".github/workflows/ci.yml", "Taskfile.yml"],
      re: /(?:GOVULNCHECK_VERSION: |govulncheck@)v(\d+\.\d+\.\d+)/,
    },
    {
      name: "Biome",
      repo: "biomejs/biome",
      tag: /^@biomejs\/biome@(\d+\.\d+\.\d+)$/,
      files: ["package.json", "biome.json"],
      re: /(?:"@biomejs\/biome":\s*"|schemas\/)(\d+\.\d+\.\d+)/,
    },
  ];
}

/** Action references such as `owner/repo@v4` or `owner/repo/sub@v0.36.0` in workflow files. */
export function actionRefs(text) {
  return [...text.matchAll(/uses:\s*([\w.-]+\/[\w.-]+)(?:\/[\w./-]+)?@(v\d+(?:\.\d+\.\d+)?)\b/g)].map((m) => ({ repo: m[1], ref: m[2] }));
}

/** Escapes every regex metacharacter, backslash included. */
export const esc = (t) => t.replace(/[\\^$.*+?()[\]{}|/]/g, "\\$&");

export function rewrite(text, re, to) {
  return text.replace(new RegExp(re.source, `${re.flags.replace("g", "")}g`), (whole, v) => whole.replace(v, to));
}

export function plan(root, tagsOf = remoteTags) {
  const items = [];
  const read = (f) => fs.readFileSync(path.join(root, f), "utf8");
  for (const t of targets()) {
    const tags = tagsOf(t.repo);
    let to = latest(tags, t.tag);
    if (!to) throw new Error(`no release tag found for ${t.name}; did upstream rename its tags?`);
    to = t.normalize ? t.normalize(to) : to;
    for (const f of t.files) {
      const m = t.re.exec(read(f));
      if (m && m[1] !== to) items.push({ name: t.name, file: f, from: m[1], to, re: t.re });
    }
  }
  const wfDir = path.join(root, ".github", "workflows");
  const seen = new Map();
  for (const f of fs.existsSync(wfDir) ? fs.readdirSync(wfDir).filter((n) => n.endsWith(".yml")) : []) {
    const file = `.github/workflows/${f}`;
    for (const { repo, ref } of actionRefs(read(file))) {
      const full = /^v\d+\.\d+\.\d+$/.test(ref);
      if (!seen.has(repo)) seen.set(repo, tagsOf(repo));
      const to = full ? `v${latest(seen.get(repo), /^v(\d+\.\d+\.\d+)$/)}` : `v${latest(seen.get(repo), /^v(\d+)$/)}`;
      if (to !== ref && !to.endsWith("null")) {
        items.push({
          name: `action ${repo}`,
          file,
          from: ref,
          to,
          re: new RegExp(`${esc(repo)}(?:/[\\w./-]+)?@(${esc(ref)})\\b`),
        });
      }
    }
  }
  return items;
}

export function apply(root, items) {
  for (const it of items) {
    const p = path.join(root, it.file);
    fs.writeFileSync(p, rewrite(fs.readFileSync(p, "utf8"), it.re, it.to));
  }
}

export function main(argv, tagsOf = remoteTags) {
  const root = argv.includes("--root") ? argv[argv.indexOf("--root") + 1] : process.cwd();
  const cmd = argv[0];
  if (cmd !== "check" && cmd !== "update") {
    console.error("usage: deps.mjs check|update [--root dir]");
    return 2;
  }
  const items = plan(root, tagsOf);
  for (const it of items) console.log(`${it.name.padEnd(36)} ${it.from} -> ${it.to}   (${it.file})`);
  if (!items.length) console.log("all pinned versions are current");
  if (cmd === "update") apply(root, items);
  return cmd === "check" && items.length ? 1 : 0;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    process.exit(main(process.argv.slice(2)));
  } catch (e) {
    console.error(e.message);
    process.exit(3);
  }
}
