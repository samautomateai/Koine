import test from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import path from "node:path";
import fs from "node:fs";
import os from "node:os";
import { fileURLToPath } from "node:url";
import { z, objectSchema } from "../sidecar/shims/schema.mjs";
import { Type } from "../sidecar/shims/typebox.mjs";
import { tool } from "../sidecar/shims/opencode-plugin.mjs";
import { StringEnum } from "../sidecar/shims/pi-ai.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const runner = path.join(here, "..", "sidecar", "runner.mjs");
const register = path.join(here, "..", "sidecar", "register.mjs");

test("zod-like schema emits JSON Schema", () => {
  const s = objectSchema({
    a: z.string().describe("A"),
    b: z.number().int().min(1).optional(),
    c: z.array(z.enum(["x", "y"])),
    d: z.boolean().default(true),
  });
  assert.deepEqual(s, {
    type: "object",
    properties: {
      a: { type: "string", description: "A" },
      b: { type: "number" },
      c: { type: "array", items: { type: "string", enum: ["x", "y"] } },
      d: { type: "boolean", default: true },
    },
    required: ["a", "c"],
  });
  assert.deepEqual(z.union([z.string(), z.null()]).toJSON(), { anyOf: [{ type: "string" }, { type: "null" }] });
  assert.deepEqual(z.record(z.string(), z.number()).toJSON(), { type: "object", additionalProperties: { type: "number" } });
  assert.equal(tool.schema, z);
  assert.deepEqual(tool({ x: 1 }), { x: 1 });
});

test("typebox shim", () => {
  const s = Type.Object({ a: Type.String(), b: Type.Optional(Type.Number()), c: Type.Array(Type.Integer()) }, { description: "d" });
  assert.deepEqual(s.required, ["a", "c"]);
  assert.equal(s.description, "d");
  assert.equal(Type.Literal("k").const, "k");
  assert.deepEqual(StringEnum(["a", "b"]), { type: "string", enum: ["a", "b"] });
});

// Drive the runner over JSON-RPC exactly like the Go host does.
function rpc(target, steps, { runtime }) {
  const args = runtime === "node" ? ["--experimental-strip-types", "--no-warnings", "--import", register, runner, target] : [runner, target];
  const child = spawn(runtime, args, { env: { ...process.env, KOINE_CWD: path.dirname(target) } });
  const out = [];
  let buf = "";
  child.stdout.on("data", (d) => {
    buf += d;
    let i;
    while ((i = buf.indexOf("\n")) >= 0) { out.push(JSON.parse(buf.slice(0, i))); buf = buf.slice(i + 1); }
  });
  return new Promise(async (resolve) => {
    let id = 0;
    const results = [];
    for (const [method, params] of steps) {
      const my = ++id;
      child.stdin.write(JSON.stringify({ jsonrpc: "2.0", id: my, method, params }) + "\n");
      for (let n = 0; n < 200 && !out.some((m) => m.id === my); n++) await new Promise((r) => setTimeout(r, 25));
      results.push(out.find((m) => m.id === my));
    }
    child.stdin.write(JSON.stringify({ jsonrpc: "2.0", method: "shutdown" }) + "\n");
    child.on("close", () => resolve({ results, notes: out.filter((m) => !m.id) }));
  });
}

const tmp = (name, src) => {
  const d = fs.mkdtempSync(path.join(os.tmpdir(), "koine-js-"));
  const f = path.join(d, name);
  fs.writeFileSync(f, src);
  return f;
};

import { spawnSync } from "node:child_process";
const has = (c) => spawnSync(c, ["--version"]).status === 0;
const runtimes = ["node", "bun"].filter(has).map((runtime) => ({ runtime }));

for (const rt of runtimes) {
  test(`runner: Pi extension (${rt.runtime})`, async () => {
    const f = tmp("pi.mjs", `
      export default function (pi) {
        pi.registerTool({ name: "t1", description: "d", parameters: { type: "object", properties: { x: { type: "string" } } },
          async execute(id, p) { return { content: [{ type: "text", text: "got " + p.x + " " + id }] }; } });
        pi.registerCommand("cmd", { description: "c", handler: async (args, ctx) => { ctx.ui.notify("notified " + args); } });
        pi.on("tool_call", async (e) => e.toolName === "bash" ? { block: true, reason: "no bash" } : undefined);
        pi.on("tool_result", async (e) => ({ content: [{ type: "text", text: e.content[0].text.toUpperCase() }] }));
        pi.on("input", async (e) => e.text === "swallow" ? { action: "handled" } : e.text === "x" ? { action: "transform", text: "y" } : undefined);
        pi.on("before_agent_start", async (e) => ({ systemPrompt: e.systemPrompt + "\\nPI-EXTRA" }));
        pi.on("throws", async () => { throw new Error("handler bug"); });
      }`);
    const { results: r, notes } = await rpc(f, [
      ["initialize", { cwd: path.dirname(f) }],
      ["tool.execute", { name: "t1", args: { x: "v" }, callId: "c9" }],
      ["tool.execute", { name: "missing", args: {} }],
      ["command.execute", { name: "cmd", args: "A" }],
      ["command.execute", { name: "nocmd", args: "" }],
      ["event", { name: "tool_call", payload: { toolName: "bash", input: {} } }],
      ["event", { name: "tool_call", payload: { toolName: "read", input: {} } }],
      ["event", { name: "tool_result", payload: { toolName: "read", output: "abc" } }],
      ["event", { name: "input", payload: { text: "swallow" } }],
      ["event", { name: "input", payload: { text: "x" } }],
      ["event", { name: "system_prompt", payload: { system: "BASE" } }],
      ["event", { name: "throws", payload: {} }],
      ["bogus", {}],
    ], rt);
    assert.deepEqual(r[0].result.events.sort(), ["input", "system_prompt", "throws", "tool_call", "tool_result"]);
    assert.equal(r[1].result.content, "got v c9");
    assert.match(r[2].error.message, /unknown tool/);
    assert.equal(r[3].result.output, "notified A");
    assert.match(r[4].error.message, /unknown command/);
    assert.deepEqual([r[5].result.block, r[5].result.reason], [true, "no bash"]);
    assert.equal(r[6].result.block, undefined);
    assert.equal(r[7].result.output, "ABC");
    assert.equal(r[8].result.block, true);
    assert.equal(r[9].result.text, "y");
    assert.equal(r[10].result.systemAppend, "PI-EXTRA");
    assert.deepEqual(r[11].result, {});
    assert.equal(r[12].error.code, -32601);
    assert.ok(notes.some((n) => n.method === "register_tool" && n.params.name === "t1"));
    assert.ok(notes.some((n) => n.method === "register_command" && n.params.name === "cmd"));
    assert.ok(notes.some((n) => n.method === "log" && /handler bug/.test(n.params.message)));
  });

  test(`runner: opencode plugin (${rt.runtime})`, async () => {
    const f = tmp("oc.mjs", `
      import { tool } from "@opencode-ai/plugin";
      export const P = async ({ directory, $ }) => ({
        tool: { shout: tool({ description: "s", args: { t: tool.schema.string().describe("T"), n: tool.schema.number().optional() },
          async execute(a, ctx) { return a.t.toUpperCase() + ":" + ctx.sessionID + ":" + (await $\`echo \${"hi there"}\`.text()).trim(); } }) },
        "tool.execute.before": async (i, o) => { if (o.args.command === "forbidden") throw new Error("plugin says no"); if (o.args.command === "rewrite") o.args.command = "echo rewritten"; },
        "tool.execute.after": async (i, o) => { o.output += "!"; },
        "chat.message": async (i, o) => { o.parts[0].text = o.parts[0].text + " (seen)"; },
        "experimental.chat.system.transform": async (i, o) => { o.system.push("OC-SYSTEM"); },
        event: async () => {},
      });`);
    const { results: r, notes } = await rpc(f, [
      ["initialize", {}],
      ["tool.execute", { name: "shout", args: { t: "abc" }, callId: "1" }],
      ["event", { name: "tool_call", payload: { toolName: "bash", input: { command: "forbidden" } } }],
      ["event", { name: "tool_call", payload: { toolName: "bash", input: { command: "rewrite" } } }],
      ["event", { name: "tool_call", payload: { toolName: "bash", input: { command: "same" } } }],
      ["event", { name: "tool_result", payload: { toolName: "bash", output: "o" } }],
      ["event", { name: "input", payload: { text: "hello" } }],
      ["event", { name: "system_prompt", payload: { system: "S" } }],
      ["event", { name: "session_start", payload: {} }],
    ], rt);
    assert.ok(r[0].result.events.includes("tool_call") && r[0].result.events.includes("session_start"));
    assert.equal(r[1].result.content, "ABC:koine-session:hi there");
    assert.deepEqual([r[2].result.block, r[2].result.reason], [true, "plugin says no"]);
    assert.deepEqual(r[3].result.args, { command: "echo rewritten" });
    assert.equal(r[4].result.args, undefined);
    assert.equal(r[5].result.output, "o!");
    assert.equal(r[6].result.text, "hello (seen)");
    assert.equal(r[7].result.systemAppend, "OC-SYSTEM");
    const reg = notes.find((n) => n.method === "register_tool");
    assert.deepEqual(reg.params.schema, { type: "object", properties: { t: { type: "string", description: "T" }, n: { type: "number" } }, required: ["t"] });
  });

  test(`runner: bad module reports error on initialize (${rt.runtime})`, async () => {
    const f = tmp("none.mjs", `export const notAFunction = 1;`);
    const { results: r } = await rpc(f, [["initialize", {}]], rt);
    assert.match(r[0].error.message, /no exported function/);
  });
}
