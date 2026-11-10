// Node ESM resolve hook: maps Pi/opencode API packages to bundled shims, and
// falls back to shims for typebox/zod only when the user has not installed them.
import { readFileSync } from "node:fs";
import path from "node:path";
import { pathToFileURL, fileURLToPath } from "node:url";

const dir = path.dirname(fileURLToPath(import.meta.url));
const map = JSON.parse(readFileSync(path.join(dir, "shims", "map.json"), "utf8"));

function shimFor(spec, table) {
  for (const k of Object.keys(table)) {
    if (spec === k || spec.startsWith(k + "/")) return pathToFileURL(path.join(dir, "shims", table[k])).href;
  }
}

export async function resolve(specifier, context, next) {
  const api = shimFor(specifier, map.api);
  if (api) return { url: api, shortCircuit: true };
  try {
    return await next(specifier, context);
  } catch (e) {
    const fb = shimFor(specifier, map.fallback);
    if (fb) return { url: fb, shortCircuit: true };
    throw e;
  }
}
