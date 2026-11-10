// Minimal zod-like builder that emits JSON Schema. Used for opencode's
// `tool.schema.*` when the real zod package is not installed.
function chain(target) {
  const proxy = new Proxy(target, {
    get(t, prop, recv) {
      if (prop in t) return Reflect.get(t, prop, recv);
      if (typeof prop === "symbol" || prop === "then") return undefined;
      return () => proxy; // min(), max(), int(), email(), trim() … are no-ops
    },
  });
  return proxy;
}

class S {
  constructor(schema, optional = false) {
    this.schema = schema;
    this.isOptional = optional;
  }
  describe(d) {
    this.schema = { ...this.schema, description: d };
    return this.self;
  }
  optional() {
    this.isOptional = true;
    return this.self;
  }
  nullable() {
    return this.self;
  }
  default(v) {
    this.schema = { ...this.schema, default: v };
    this.isOptional = true;
    return this.self;
  }
  toJSON() {
    return this.schema;
  }
}

function mk(schema) {
  const s = new S(schema);
  s.self = chain(s);
  return s.self;
}

const jsonOf = (v) => (v && typeof v.toJSON === "function" ? v.toJSON() : (v ?? {}));

export const z = {
  string: () => mk({ type: "string" }),
  number: () => mk({ type: "number" }),
  int: () => mk({ type: "integer" }),
  boolean: () => mk({ type: "boolean" }),
  any: () => mk({}),
  unknown: () => mk({}),
  null: () => mk({ type: "null" }),
  literal: (v) => mk({ const: v }),
  enum: (vals) => mk({ type: "string", enum: [...vals] }),
  array: (item) => mk({ type: "array", items: jsonOf(item) }),
  union: (list) => mk({ anyOf: list.map(jsonOf) }),
  record: (_k, v) => mk({ type: "object", additionalProperties: jsonOf(v) }),
  object: (shape = {}) => mk(objectSchema(shape)),
};

export function objectSchema(shape) {
  const properties = {};
  const required = [];
  for (const [k, v] of Object.entries(shape)) {
    properties[k] = jsonOf(v);
    if (!v?.isOptional) required.push(k);
  }
  const out = { type: "object", properties };
  if (required.length) out.required = required;
  return out;
}
export { S, jsonOf };
