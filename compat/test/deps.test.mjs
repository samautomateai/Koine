import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { actionRefs, latest, main, plan, rewrite } from "../../scripts/deps.mjs";

const TAGS = {
  "golang/go": ["go1.26.9", "go1.27", "go1.27.4", "go1.28rc1", "go1.27.10", "weekly.2012-03-13"],
  "golangci/golangci-lint": ["v2.14.0", "v2.15.0", "v2.16.0-rc.1"],
  "golang/vuln": ["v1.9.0", "v1.10.0"],
  "biomejs/biome": ["@biomejs/biome@2.6.6", "@biomejs/biome@2.6.10", "@biomejs/js-api@9.0.0"],
  "actions/checkout": ["v6", "v7", "v7.1.0"],
  "aquasecurity/trivy-action": ["v0.35.0", "v0.36.0", "v0.37.0"],
};
const tagsOf = (repo) => TAGS[repo] ?? [];

function tree(files) {
  const d = fs.mkdtempSync(path.join(os.tmpdir(), "koine-deps-"));
  for (const [f, text] of Object.entries(files)) {
    fs.mkdirSync(path.dirname(path.join(d, f)), { recursive: true });
    fs.writeFileSync(path.join(d, f), text);
  }
  return d;
}
const files = () => ({
  "go.mod": "module x\n\ngo 1.27.4\n",
  "desktop/go.mod": "module d\n\ngo 1.26.0\n",
  "tui-bubbletea/go.mod": "module t\n\ngo 1.27.10\n",
  ".github/workflows/ci.yml":
    "env:\n  GOLANGCI_LINT_VERSION: v2.15.0\n  GOVULNCHECK_VERSION: v1.9.0\n" +
    "steps:\n  - uses: actions/checkout@v7\n  - uses: aquasecurity/trivy-action@v0.36.0\n",
  "Taskfile.yml": "- go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...\n",
  "package.json": '{"devDependencies":{"@biomejs/biome":"2.6.6"}}',
  "biome.json": '{"$schema":"https://biomejs.dev/schemas/2.6.6/schema.json"}',
});

test("latest ignores pre-releases, other packages and non-semver tags, and compares numerically", () => {
  assert.equal(latest(TAGS["golang/go"], /^go(\d+\.\d+(?:\.\d+)?)$/), "1.27.10");
  assert.equal(latest(TAGS["golangci/golangci-lint"], /^v(\d+\.\d+\.\d+)$/), "2.15.0");
  assert.equal(latest(TAGS["biomejs/biome"], /^@biomejs\/biome@(\d+\.\d+\.\d+)$/), "2.6.10");
  assert.equal(latest([], /^v(\d+)$/), null);
});

test("actionRefs finds major and full-version references, including sub-paths", () => {
  const refs = actionRefs("- uses: actions/checkout@v7\n  uses: github/codeql-action/init@v4\n- uses: x/y@abc123\n");
  assert.deepEqual(refs, [
    { repo: "actions/checkout", ref: "v7" },
    { repo: "github/codeql-action", ref: "v4" },
  ]);
});

test("rewrite replaces only the version inside the match", () => {
  assert.equal(rewrite("a: v2.15.0\nb: v2.15.0", /a: v(\d+\.\d+\.\d+)/, "2.16.0"), "a: v2.16.0\nb: v2.15.0");
});

test("plan finds every outdated pin and skips current ones", () => {
  const d = tree(files());
  const got = plan(d, tagsOf).map((i) => `${i.name} ${i.from}->${i.to} ${i.file}`);
  assert.ok(got.includes("Go toolchain 1.27.4->1.27.10 go.mod"));
  assert.ok(got.includes("Go toolchain 1.26.0->1.27.10 desktop/go.mod"));
  assert.ok(got.includes("golangci-lint 2.15.0->2.15.0 .github/workflows/ci.yml") === false);
  assert.ok(got.includes("govulncheck 1.9.0->1.10.0 Taskfile.yml"));
  assert.ok(got.includes("Biome 2.6.6->2.6.10 biome.json"));
  assert.ok(got.includes("action aquasecurity/trivy-action v0.36.0->v0.37.0 .github/workflows/ci.yml"));
  assert.ok(!got.some((g) => g.includes("tui-bubbletea")), "already current");
  fs.rmSync(d, { recursive: true, force: true });
});

test("main: check exits 1 when outdated, update rewrites files, then check passes", () => {
  const d = tree(files());
  const log = console.log;
  console.log = () => {};
  try {
    assert.equal(main(["check", "--root", d], tagsOf), 1);
    assert.equal(main(["update", "--root", d], tagsOf), 0);
    assert.equal(main(["check", "--root", d], tagsOf), 0);
    assert.match(fs.readFileSync(path.join(d, "go.mod"), "utf8"), /^go 1\.27\.10$/m);
    assert.match(fs.readFileSync(path.join(d, ".github/workflows/ci.yml"), "utf8"), /trivy-action@v0\.37\.0/);
    assert.match(fs.readFileSync(path.join(d, "package.json"), "utf8"), /"2\.6\.10"/);
    assert.equal(main(["bogus"], tagsOf), 2);
  } finally {
    console.log = log;
    fs.rmSync(d, { recursive: true, force: true });
  }
});

test("plan fails loudly when upstream tags can no longer be parsed", () => {
  const d = tree(files());
  assert.throws(() => plan(d, () => []), /no release tag found/);
  fs.rmSync(d, { recursive: true, force: true });
});
