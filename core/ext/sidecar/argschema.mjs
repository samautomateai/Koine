// Turn the argument declarations of an opencode tool (zod-like builders, or real zod) into JSON Schema.

let zodMod;
async function loadZod() {
  if (zodMod !== undefined) return zodMod;
  try {
    zodMod = await import("zod");
  } catch {
    zodMod = null;
  }
  return zodMod;
}
async function schemaOf(v) {
  if (v && typeof v.toJSON === "function") return v.toJSON();
  const z = await loadZod();
  if (v && z && typeof z.toJSONSchema === "function") {
    try {
      const rest = { ...z.toJSONSchema(v) };
      delete rest.$schema;
      return rest;
    } catch {}
  }
  return {};
}
export async function argsToSchema(args) {
  if (!args) return { type: "object", properties: {} };
  const properties = {};
  const required = [];
  for (const [k, v] of Object.entries(args)) {
    properties[k] = await schemaOf(v);
    const opt = v && (v.isOptional === true || (typeof v.isOptional === "function" && v.isOptional()));
    if (!opt) required.push(k);
  }
  const s = { type: "object", properties };
  if (required.length) s.required = required;
  return s;
}
