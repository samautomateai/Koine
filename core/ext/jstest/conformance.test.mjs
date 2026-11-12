// Manifest-driven conformance: every item in compat/pi.json is exercised against
// the real runner, so the manifest cannot claim more (or less) than the code does.
//   verified    -> exists, and using it reports NO compat note
//   inert       -> exists, does not throw, reports an "inert" note
//   unsupported -> does not throw, reports an "unsupported" note
import test from "node:test";
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import path from "node:path";
import fs from "node:fs";
import os from "node:os";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const sidecar = path.join(here, "..", "sidecar");
const manifest = JSON.parse(fs.readFileSync(path.join(here, "..", "..", "..", "compat", "pi.json"), "utf8"));
const items = manifest.items;
const has = (c) => spawnSync(c, ["--version"]).status === 0;

function rpc(target, steps, runtime) {
  const args =
    runtime === "node"
      ? [
          "--experimental-strip-types",
          "--no-warnings",
          "--import",
          path.join(sidecar, "register.mjs"),
          path.join(sidecar, "runner.mjs"),
          target,
        ]
      : [path.join(sidecar, "runner.mjs"), target];
  const child = spawn(runtime, args, { env: { ...process.env, PIGO_CWD: path.dirname(target), KOINE_CWD: path.dirname(target) } });
  const out = [];
  let buf = "";
  child.stdout.on("data", (d) => {
    buf += d;
    for (let i = buf.indexOf("\n"); i >= 0; i = buf.indexOf("\n")) {
      out.push(JSON.parse(buf.slice(0, i)));
      buf = buf.slice(i + 1);
    }
  });
  const closed = new Promise((resolve) => child.on("close", resolve));
  return (async () => {
    let id = 0;
    const results = [];
    for (const [method, params] of steps) {
      const my = ++id;
      child.stdin.write(JSON.stringify({ jsonrpc: "2.0", id: my, method, params }) + "\n");
      for (let n = 0; n < 300 && !out.some((m) => m.id === my); n++) await new Promise((r) => setTimeout(r, 25));
      results.push(out.find((m) => m.id === my));
    }
    child.stdin.write(JSON.stringify({ jsonrpc: "2.0", method: "shutdown" }) + "\n");
    await closed;
    return { results, notes: out.filter((m) => m.id === undefined) };
  })();
}

const tmp = (name, src) => {
  const d = fs.mkdtempSync(path.join(os.tmpdir(), "koine-conf-"));
  const f = path.join(d, name);
  fs.writeFileSync(f, src);
  return f;
};

for (const runtime of ["node", "bun"].filter(has)) {
  test(`conformance: API surface matches compat manifest (${runtime})`, async () => {
    const calls = items.filter((i) => ["api", "ctx", "ui"].includes(i.kind) && i.shape);
    const probes = JSON.stringify(calls.map((i) => ({ kind: i.kind, name: i.name, shape: i.shape })));
    // The extension probes every member inside try/catch; "verified" members may throw on empty args — that is fine.
    const f = tmp(
      "probe.mjs",
      `
      const probes = ${probes};
      const resolve = (root, dotted) => {
        const parts = dotted.split(".");
        let o = root;
        for (const p of parts.slice(1, -1)) o = o?.[p];
        return [o, parts.at(-1)];
      };
      const SKIP_CALL = new Set(["pi.on", "pi.registerTool", "pi.registerCommand", "pi.exec", "pi.sendUserMessage"]);
      export default function (pi) {
        pi.on("turn_start", async (_e, ctx) => {
          const roots = { api: pi, ctx, ui: ctx };
          const present = {};
          for (const p of probes) {
            const [o, key] = resolve(roots[p.kind], p.name);
            try {
              if (p.shape === "prop") present[p.name] = o?.[key] !== undefined;
              else {
                const fn = o?.[key];
                present[p.name] = typeof fn === "function";
                if (!SKIP_CALL.has(p.name)) {
                  try {
                    const r = fn.call(o);
                    if (r && r.catch) r.catch(() => {});
                  } catch {}
                }
              }
            } catch (e) { present[p.name] = "threw:" + e.message; }
          }
          pi.sendUserMessage("PRESENT:" + JSON.stringify(present));
        });
      }`,
    );
    const { results, notes } = await rpc(
      f,
      [
        ["initialize", { trusted: true }],
        ["event", { name: "turn_start", payload: { turn: 0 } }],
      ],
      runtime,
    );
    assert.equal(results[0].error, undefined);
    assert.equal(results[1].error, undefined);
    const present = JSON.parse(notes.find((n) => n.method === "send_user_message").params.text.slice("PRESENT:".length));
    const compat = new Map();
    for (const n of notes.filter((n) => n.method === "compat")) compat.set(n.params.api, n.params.kind);

    const bad = [];
    for (const i of calls) {
      const got = compat.get(i.name);
      if (String(present[i.name]).startsWith("threw")) {
        bad.push(`${i.name}: threw (${present[i.name]})`);
        continue;
      }
      if (i.shape === "prop") {
        // properties never report (reading is not a call); what matters is the value they evaluate to
        const wantPresent = i.status === "verified" || i.behavior === "identity" || (i.status === "inert" && i.behavior == null);
        if (!!present[i.name] !== wantPresent)
          bad.push(
            `${i.name}: property should ${wantPresent ? "be defined" : "evaluate to undefined"} (status ${i.status}${i.behavior ? ", " + i.behavior : ""})`,
          );
        continue;
      }
      if (i.status === "verified") {
        if (!present[i.name]) bad.push(`${i.name}: verified but missing`);
        if (got) bad.push(`${i.name}: verified but reported ${got}`);
      } else if (i.status === "inert" || i.status === "unsupported") {
        if (got !== i.status) bad.push(`${i.name}: manifest says ${i.status}, observed ${got ?? "no note"}`);
      }
    }
    assert.deepEqual(bad, [], "manifest and runner disagree:\n" + bad.join("\n"));
  });

  test(`conformance: verified events reach Pi handlers (${runtime})`, async () => {
    const evs = items.filter((i) => i.kind === "event" && i.status === "verified");
    const f = tmp(
      "events.mjs",
      `
      const names = ${JSON.stringify(evs.map((e) => e.name))};
      export default function (pi) { for (const n of names) pi.on(n, async (e) => { pi.sendUserMessage("GOT:" + n + ":" + e.type); }); }`,
    );
    const steps = [["initialize", {}]];
    for (const e of evs) {
      const payload =
        {
          tool_call: { toolName: "bash", input: {} },
          tool_result: { toolName: "bash", output: "o" },
          input: { text: "x" },
          system_prompt: { system: "S" },
        }[e.host ?? e.name] ?? {};
      steps.push(["event", { name: e.host ?? e.name, payload }]);
    }
    const { results, notes } = await rpc(f, steps, runtime);
    assert.ok(results[0].result.events.length >= evs.length - 1, "subscription list");
    const got = notes.filter((n) => n.method === "send_user_message").map((n) => n.params.text);
    for (const e of evs)
      assert.ok(
        got.some((g) => g.startsWith("GOT:" + e.name + ":" + e.name)),
        `Pi event ${e.name} was not delivered; got ${got}`,
      );
  });

  test(`conformance: resources_discover returns skill, prompt and theme paths (${runtime})`, async () => {
    const f = tmp(
      "res.mjs",
      `
      export default function (pi) {
        pi.on("resources_discover", async () => ({
          skillPaths: ["skills", "/abs/skills", 7, ""],
          promptPaths: ["p/a.md"],
          themePaths: ["themes"],
        }));
        pi.on("resources_discover", async (e) => { pi.sendUserMessage("SAW:" + e.reason + ":" + e.cwd); });
      }`,
    );
    const dir = path.dirname(f);
    const { results, notes } = await rpc(
      f,
      [
        ["initialize", {}],
        ["event", { name: "resources_discover", payload: { cwd: "/work", reason: "reload" } }],
        ["event", { name: "resources_discover", payload: { cwd: "/work", reason: "startup" } }],
      ],
      runtime,
    );
    assert.ok(results[0].result.events.includes("resources_discover"), "the extension subscribes to the event");
    assert.deepEqual(results[1].result.resources, {
      skillPaths: [path.join(dir, "skills"), "/abs/skills"],
      promptPaths: [path.join(dir, "p", "a.md")],
      themePaths: [path.join(dir, "themes")],
    });
    const saw = notes.filter((n) => n.method === "send_user_message").map((n) => n.params.text);
    assert.deepEqual(saw, ["SAW:reload:/work", "SAW:startup:/work"], "handlers get cwd and the reason");
  });

  test(`conformance: resources_discover without paths answers nothing (${runtime})`, async () => {
    const f = tmp("res2.mjs", `export default function (pi) { pi.on("resources_discover", async () => undefined); }`);
    const { results } = await rpc(
      f,
      [
        ["initialize", {}],
        ["event", { name: "resources_discover", payload: {} }],
      ],
      runtime,
    );
    assert.equal(results[1].result.resources, undefined);
  });

  test(`conformance: all verified module specifiers resolve (${runtime})`, async () => {
    const specs = items.filter((i) => i.kind === "import" && i.status === "verified").map((i) => i.name);
    const f = tmp(
      "imports.mjs",
      specs.map((s, n) => `import * as m${n} from ${JSON.stringify(s)};`).join("\n") +
        `\nexport default function (pi) { pi.sendUserMessage("LOADED:" + ${specs.length}); }`,
    );
    const { results, notes } = await rpc(f, [["initialize", {}]], runtime);
    assert.equal(results[0].error, undefined, JSON.stringify(results[0].error));
    assert.ok(notes.some((n) => n.method === "send_user_message" && n.params.text === "LOADED:" + specs.length));
  });

  test(`conformance: unknown future API never crashes an extension (${runtime})`, async () => {
    const f = tmp(
      "future.mjs",
      `
      export default function (pi) {
        pi.brandNewApi({ a: 1 }); pi.another.deep.chain();
        pi.on("tool_call", async (e, ctx) => {
          ctx.ui.newWidgetType(1);
          const v = ctx.somethingNew;
          ctx.ui.somethingElse.fg("x");
          const styled = ctx.ui.theme.fg("accent", "kept");
          pi.sendUserMessage("STYLED:" + styled);
        });
        pi.sendUserMessage("LOADED");
      }`,
    );
    const { results, notes } = await rpc(
      f,
      [
        ["initialize", {}],
        ["event", { name: "tool_call", payload: { toolName: "x", input: {} } }],
      ],
      runtime,
    );
    assert.equal(results[0].error, undefined);
    assert.equal(results[1].error, undefined);
    const apis = notes.filter((n) => n.method === "compat").map((n) => n.params.api);
    for (const a of ["pi.brandNewApi", "pi.another.deep.chain", "ctx.ui.newWidgetType", "ctx.ui.somethingElse.fg"])
      assert.ok(apis.includes(a), `${a} not reported: ${apis}`);
    assert.ok(
      notes.some((n) => n.method === "send_user_message" && n.params.text === "STYLED:kept"),
      "ui.theme helpers must return text unstyled",
    );
  });
}
