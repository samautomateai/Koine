// Event dispatch: what Pi handlers and opencode hooks receive for a host event, and how their answers become a Decision (SPEC §2).
//
// Errors: a handler that throws is isolated. The error is reported to the host (extension_error) and the next handler runs. The one
// exception is tool_call, a gate: a throwing handler blocks the call, as Pi does.
import path from "node:path";
import { notify, state, textOf, SESSION } from "./host.mjs";
import { piCtx } from "./ctx.mjs";

const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);

// resources_discover: a handler returns { skillPaths, promptPaths, themePaths }. Relative paths are taken from the extension's own directory.
function collectResources(r, d) {
  const dir = path.dirname(state.target);
  const paths = (v) =>
    Array.isArray(v)
      ? v.filter((x) => typeof x === "string" && x.trim()).map((x) => (path.isAbsolute(x) || x.startsWith("~") ? x : path.resolve(dir, x)))
      : [];
  d.resources ??= { skillPaths: [], promptPaths: [], themePaths: [] };
  for (const k of ["skillPaths", "promptPaths", "themePaths"]) d.resources[k].push(...paths(r[k]));
}

const contentOf = (p) => (Array.isArray(p.content) ? structuredClone(p.content) : [{ type: "text", text: p.output ?? "" }]);

// The event object a Pi handler gets. It is one object for the whole chain of handlers, so a change one makes is seen by the next.
function eventFor(name, p) {
  switch (name) {
    case "tool_call":
      return { type: "tool_call", toolName: p.toolName, toolCallId: p.callId, input: structuredClone(p.input ?? {}) };
    case "tool_result":
      return {
        type: "tool_result",
        toolName: p.toolName,
        toolCallId: p.callId,
        input: p.input,
        content: contentOf(p),
        details: p.details ?? {},
        isError: !!p.isError,
      };
    case "input":
      return {
        type: "input",
        text: p.text ?? "",
        images: p.images ?? [],
        source: p.source ?? "interactive",
        streamingBehavior: p.streamingBehavior,
      };
    case "before_agent_start":
      return {
        type: "before_agent_start",
        messageType: "user",
        prompt: p.prompt ?? "",
        images: p.images ?? [],
        systemPrompt: p.systemPrompt ?? "",
        systemPromptOptions: p.systemPromptOptions ?? {},
      };
    default:
      return { type: name, ...p };
  }
}

// What one handler's answer changes. It returns true when the chain of handlers ends here.
function apply(name, r, ev, d) {
  if (name === "tool_call" && r.block) {
    d.block = true;
    d.reason = r.reason ?? "blocked";
    if (r.terminate) d.terminate = true;
    return true;
  }
  if (name.startsWith("session_before_")) {
    if (r.cancel) {
      d.cancel = true;
      return true;
    }
    return false;
  }
  switch (name) {
    case "resources_discover":
      collectResources(r, d);
      break;
    case "tool_result":
      // a patch: fields the handler leaves out keep their value, and the next handler sees the result so far
      if (r.content !== undefined) ev.content = Array.isArray(r.content) ? r.content : [{ type: "text", text: textOf(r.content) }];
      if (r.details !== undefined) ev.details = r.details;
      if (typeof r.isError === "boolean") ev.isError = r.isError;
      break;
    case "input":
      if (r.action === "handled") {
        d.handled = true;
        return true;
      }
      if (r.action === "transform") {
        if (typeof r.text === "string") ev.text = r.text;
        if (Array.isArray(r.images)) ev.images = r.images;
      }
      break;
    case "before_agent_start":
      if (typeof r.systemPrompt === "string") ev.systemPrompt = r.systemPrompt;
      if (r.message && typeof r.message === "object") {
        const m = r.message;
        d.message = {
          customType: String(m.customType ?? ""),
          content: typeof m.content === "string" ? m.content : textOf(m.content),
          display: !!m.display,
          details: m.details,
        };
      }
      break;
  }
  return false;
}

// After every handler ran: what differs from what the host sent becomes the Decision.
function finish(name, p, ev, d) {
  switch (name) {
    case "tool_call":
      if (!d.block && !same(ev.input, p.input ?? {})) d.args = ev.input;
      break;
    case "tool_result": {
      const before = contentOf(p);
      if (!same(ev.content, before)) {
        d.content = ev.content;
        d.output = textOf(ev.content);
      }
      if (ev.isError !== !!p.isError) d.isError = ev.isError;
      if (!same(ev.details, p.details ?? {})) d.details = ev.details;
      break;
    }
    case "input":
      if (!d.handled) {
        if (ev.text !== (p.text ?? "")) d.text = ev.text;
        if (!same(ev.images, p.images ?? [])) d.images = ev.images;
      }
      break;
    case "before_agent_start":
      if (ev.systemPrompt !== (p.systemPrompt ?? "")) d.systemPrompt = ev.systemPrompt;
      break;
  }
}

export async function piEvent(name, p, d, signal) {
  const hs = state.handlers.get(name);
  if (!hs) return;
  const ev = eventFor(name, p);
  for (const h of hs) {
    let r;
    try {
      r = await h(ev, piCtx(null, signal));
    } catch (e) {
      const error = String(e?.message ?? e);
      notify("ext_error", { event: name, error });
      if (name === "tool_call") {
        d.block = true;
        d.reason = `Extension error in tool_call handler: ${error}`;
        return;
      }
      continue;
    }
    if (!r || typeof r !== "object") continue;
    if (apply(name, r, ev, d)) return;
  }
  finish(name, p, ev, d);
}

const OC_EVENT = { session_start: "session.created", agent_end: "session.idle" };

export async function ocEvent(name, p, d) {
  for (const h of state.oc) {
    try {
      if (name === "tool_call" && h["tool.execute.before"]) {
        const output = { args: structuredClone(p.input ?? {}) };
        try {
          await h["tool.execute.before"]({ tool: p.toolName, sessionID: SESSION, callID: p.callId }, output);
        } catch (e) {
          d.block = true;
          d.reason = String(e?.message ?? e);
          return;
        }
        if (JSON.stringify(output.args) !== JSON.stringify(p.input)) d.args = output.args;
      } else if (name === "tool_result" && h["tool.execute.after"]) {
        const output = { title: p.toolName, output: p.output, metadata: {} };
        await h["tool.execute.after"]({ tool: p.toolName, sessionID: SESSION, callID: p.callId }, output);
        if (output.output !== p.output) d.output = String(output.output);
      } else if (name === "input" && h["chat.message"]) {
        const output = { message: { role: "user" }, parts: [{ type: "text", text: p.text }] };
        await h["chat.message"]({ sessionID: SESSION }, output);
        const t = output.parts?.[0]?.text;
        if (typeof t === "string" && t !== p.text) d.text = t;
      } else if (name === "system_prompt" && h["experimental.chat.system.transform"]) {
        const output = { system: [] };
        await h["experimental.chat.system.transform"]({}, output);
        if (output.system.length) d.systemAppend = output.system.join("\n");
      }
      if (OC_EVENT[name] && typeof h.event === "function") await h.event({ event: { type: OC_EVENT[name], properties: {} } });
    } catch (e) {
      notify("ext_error", { event: name, error: String(e?.message ?? e) });
    }
  }
}

export async function onEvent({ name, payload }, signal) {
  const d = {};
  await piEvent(name, payload ?? {}, d, signal);
  if (!d.block && !d.handled && !d.cancel) await ocEvent(name, payload ?? {}, d);
  return d;
}
