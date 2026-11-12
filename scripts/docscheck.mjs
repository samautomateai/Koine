#!/usr/bin/env node
// Checks that requirements, design, ADRs and links stay consistent with each other and with the code.
//   node scripts/docscheck.mjs [--root dir] [--base <git ref>] [--docs-impact-none]
// --base also enforces the living-docs rule: product code changed since <ref> needs a docs change,
// unless --docs-impact-none (or DOCS_IMPACT_NONE=1) is given, or $PR_BODY says "Docs-Impact: none — <reason>" with a real reason.
import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";

const STATUSES = new Set(["planned", "implemented", "deprecated"]);
const ADR_STATUSES = /^(proposed|accepted|superseded by ADR \d{4}|deprecated)$/;
const SKIP = new Set(["node_modules", ".git", "bin", "vendor"]);

function* walk(dir, test) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    if (SKIP.has(e.name)) continue;
    const p = path.join(dir, e.name);
    if (e.isDirectory()) yield* walk(p, test);
    else if (test(e.name)) yield p;
  }
}

const list = (v) =>
  (v ?? "")
    .split(";")
    .map((s) => s.trim())
    .filter((s) => s && s.toLowerCase() !== "none" && !s.toLowerCase().startsWith("none "));

/** Sections that start with `### REQ-…` / `## DES-…`, each with `- Key: value` lines. */
export function parseSections(md, idPattern) {
  const out = [];
  let cur = null;
  md.split("\n").forEach((line, i) => {
    const h = new RegExp(`^#{2,3}\\s+(${idPattern})\\s+[—-]\\s+(.*)$`).exec(line);
    if (h) {
      cur = { id: h[1], title: h[2].trim(), line: i + 1, fields: {} };
      out.push(cur);
      return;
    }
    if (/^#{1,3}\s/.test(line)) cur = null;
    const f = cur && /^- ([A-Za-z]+):\s*(.*)$/.exec(line);
    if (f) cur.fields[f[1].toLowerCase()] = f[2];
  });
  return out;
}

export function goTests(root) {
  const names = new Set();
  for (const f of walk(root, (n) => n.endsWith("_test.go"))) {
    for (const m of fs.readFileSync(f, "utf8").matchAll(/^func (Test\w+)\(/gm)) names.add(m[1]);
  }
  return names;
}

export function jsTitles(root) {
  const titles = [];
  for (const f of walk(root, (n) => n.endsWith(".test.mjs"))) {
    for (const m of fs.readFileSync(f, "utf8").matchAll(/\btest\(\s*[`"']([^`"'\n]*)/g))
      titles.push(m[1].replace(/\$\{[^}]*\}.*$/, "").trim());
  }
  return titles;
}

export function checkRequirements(reqs, designs, tests, titles) {
  const errs = [];
  const ids = new Set();
  const designIds = new Set(designs.map((d) => d.id));
  for (const r of reqs) {
    const at = `requirements.md:${r.line} ${r.id}`;
    if (ids.has(r.id)) errs.push(`${at}: duplicate id`);
    ids.add(r.id);
    const status = r.fields.status;
    if (!STATUSES.has(status)) errs.push(`${at}: status must be one of ${[...STATUSES].join(", ")} (got "${status ?? ""}")`);
    if (!r.fields.statement) errs.push(`${at}: needs a Statement`);
    const des = list(r.fields.design);
    for (const d of des) if (!designIds.has(d)) errs.push(`${at}: design ${d} does not exist`);
    if (status === "implemented") {
      if (!des.length) errs.push(`${at}: implemented requirements need a Design reference`);
      const ts = list(r.fields.tests);
      if (!ts.length) errs.push(`${at}: implemented requirements need Tests`);
      for (const t of ts) {
        if (t.startsWith("js:")) {
          const want = t.slice(3).trim();
          if (!titles.some((x) => x.startsWith(want))) errs.push(`${at}: no JS test title starts with "${want}"`);
        } else if (!tests.has(t)) errs.push(`${at}: Go test ${t} does not exist`);
      }
    }
  }
  return errs;
}

export function checkDesign(designs, reqs, root) {
  const errs = [];
  const reqIds = new Set(reqs.map((r) => r.id));
  const seen = new Set();
  for (const d of designs) {
    const at = `design.md:${d.line} ${d.id}`;
    if (seen.has(d.id)) errs.push(`${at}: duplicate id`);
    seen.add(d.id);
    const sat = list(d.fields.satisfies);
    if (!sat.length) errs.push(`${at}: needs Satisfies`);
    for (const r of sat) if (!reqIds.has(r)) errs.push(`${at}: requirement ${r} does not exist`);
    const code = list(d.fields.code);
    if (!code.length) errs.push(`${at}: needs Code paths`);
    for (const c of code) if (!fs.existsSync(path.join(root, c))) errs.push(`${at}: code path ${c} does not exist`);
  }
  for (const r of reqs) {
    if (r.fields.status !== "implemented") continue;
    for (const d of list(r.fields.design)) {
      const des = designs.find((x) => x.id === d);
      if (des && !list(des.fields.satisfies).includes(r.id))
        errs.push(`${r.id}: lists ${d}, but ${d} does not list ${r.id} under Satisfies`);
    }
  }
  return errs;
}

export function checkAdrs(root, reqs) {
  const errs = [];
  const dir = path.join(root, "docs", "adr");
  if (!fs.existsSync(dir)) return errs;
  const reqIds = new Set(reqs.map((r) => r.id));
  const files = fs.readdirSync(dir).filter((n) => /^\d{4}-.*\.md$/.test(n) && !n.startsWith("0000"));
  const byNumber = new Map();
  for (const f of files) byNumber.set(f.slice(0, 4), [...(byNumber.get(f.slice(0, 4)) ?? []), f]);
  for (const [n, fs2] of byNumber) if (fs2.length > 1) errs.push(`adr: number ${n} is used by ${fs2.join(", ")}; renumber one`);
  for (const f of files) {
    const text = fs.readFileSync(path.join(dir, f), "utf8");
    const status = /^- Status:\s*(.*)$/m.exec(text)?.[1]?.trim();
    if (!status || !ADR_STATUSES.test(status))
      errs.push(`adr/${f}: Status must be proposed, accepted, deprecated or "superseded by ADR NNNN"`);
    if (!/^- Date:\s*\d{4}-\d{2}-\d{2}$/m.test(text)) errs.push(`adr/${f}: needs "- Date: YYYY-MM-DD"`);
    for (const r of list(/^- Requirements:\s*(.*)$/m.exec(text)?.[1]))
      if (!reqIds.has(r)) errs.push(`adr/${f}: requirement ${r} does not exist`);
  }
  return errs;
}

export function checkLinks(root) {
  const errs = [];
  const files = [...walk(root, (n) => n.endsWith(".md"))].filter((f) => !f.includes(`${path.sep}testdata${path.sep}`));
  for (const f of files) {
    const text = fs.readFileSync(f, "utf8").replace(/```[\s\S]*?```/g, "");
    for (const m of text.matchAll(/\[[^\]]*\]\(([^)\s]+)\)/g)) {
      const target = m[1].split("#")[0];
      if (!target || /^(https?:|mailto:)/.test(target)) continue;
      if (!fs.existsSync(path.resolve(path.dirname(f), target))) errs.push(`${path.relative(root, f)}: broken link ${m[1]}`);
    }
  }
  return errs;
}

const isTest = (f) => /(_test\.go|\.test\.mjs)$/.test(f) || /(^|\/)(testdata|fixture)\//.test(f);
const isProduct = (f) =>
  /^(core|cmd|scripts|compat)\//.test(f) && /\.(go|mjs|js|json|sh)$/.test(f) && !isTest(f) && !/^compat\/(pi|settings)\.json$/.test(f);
/** Files that record what Koine does and how far it matches Pi: requirements, design, ADRs, spec, gap tables, compatibility manifests. */
const isTracker = (f) => /^docs\//.test(f) || /^compat\/([A-Z]+\.md|pi\.json|settings\.json)$/.test(f);

/**
 * "Docs-Impact: none — <reason>" in a PR body opts out of the living-docs rule, but only with a real reason (a bare "none" or the
 * template's placeholder does not count).
 */
export function parseDocsImpact(body) {
  const m = /^Docs-Impact:[ \t]*none[ \t]*[-—–:]?[ \t]*(.*)$/im.exec(body ?? "");
  const reason = (m?.[1] ?? "").trim();
  return { none: Boolean(m) && reason.length >= 8 && !/^<.*>$/.test(reason), reason };
}

/**
 * Living-docs rule (docs/CHECKLIST.md): product code changed ⇒ CHANGELOG.md has a line AND a requirement, design, SPEC, gap table or
 * compatibility manifest changed too, unless the PR states why no documentation is affected.
 */
export function livingDocsViolation(changed, impactNone) {
  const product = changed.filter(isProduct);
  if (!product.length || impactNone) return null;
  const missing = [];
  if (!changed.includes("CHANGELOG.md")) missing.push("a line in CHANGELOG.md");
  if (!changed.some(isTracker))
    missing.push("an update to the requirement, design, SPEC, gap table or compat manifest it affects (docs/ or compat/)");
  if (!missing.length) return null;
  return (
    `product code changed (${product.slice(0, 3).join(", ")}${product.length > 3 ? ", …" : ""}) but there is no ${missing.join(" and no ")}. ` +
    `Work through docs/CHECKLIST.md; if nothing needs updating, put "Docs-Impact: none — <specific reason>" in the PR description.`
  );
}

export function run({ root, base, impactNone }) {
  const read = (p) => fs.readFileSync(path.join(root, p), "utf8");
  const reqs = parseSections(read("docs/requirements.md"), "REQ-[A-Z]+-\\d{3}");
  const designs = parseSections(read("docs/design.md"), "DES-\\d{3}");
  const errs = [
    ...(reqs.length ? [] : ["requirements.md: no requirements found"]),
    ...checkRequirements(reqs, designs, goTests(root), jsTitles(root)),
    ...checkDesign(designs, reqs, root),
    ...checkAdrs(root, reqs),
    ...checkLinks(root),
  ];
  if (base) {
    const changed = execFileSync("git", ["diff", "--name-only", `${base}...HEAD`], { cwd: root, encoding: "utf8" })
      .split("\n")
      .filter(Boolean);
    const v = livingDocsViolation(changed, impactNone);
    if (v) errs.push(v);
  }
  return { errs, counts: { requirements: reqs.length, design: designs.length } };
}

export function main(argv) {
  const arg = (k) => argv[argv.indexOf(k) + 1];
  const root = argv.includes("--root") ? arg("--root") : process.cwd();
  const impactNone =
    argv.includes("--docs-impact-none") || process.env.DOCS_IMPACT_NONE === "1" || parseDocsImpact(process.env.PR_BODY).none;
  const { errs, counts } = run({ root, base: argv.includes("--base") ? arg("--base") : undefined, impactNone });
  for (const e of errs) console.log("✗ " + e);
  console.error(
    errs.length
      ? `${errs.length} documentation problem(s)`
      : `docs ok: ${counts.requirements} requirements, ${counts.design} design sections`,
  );
  return errs.length ? 1 : 0;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) process.exit(main(process.argv.slice(2)));
