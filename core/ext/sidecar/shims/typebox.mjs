// Minimal TypeBox: builds plain JSON Schema objects.
const optionalSet = new WeakSet();
const opts = (s, o) => (o ? { ...s, ...o } : s);

export const Type = {
  String: (o) => opts({ type: "string" }, o),
  Number: (o) => opts({ type: "number" }, o),
  Integer: (o) => opts({ type: "integer" }, o),
  Boolean: (o) => opts({ type: "boolean" }, o),
  Null: (o) => opts({ type: "null" }, o),
  Any: (o) => opts({}, o),
  Unknown: (o) => opts({}, o),
  Literal: (v, o) => opts({ const: v }, o),
  Array: (items, o) => opts({ type: "array", items }, o),
  Union: (anyOf, o) => opts({ anyOf }, o),
  Record: (_k, v, o) => opts({ type: "object", additionalProperties: v }, o),
  Optional: (s) => {
    const c = { ...s };
    optionalSet.add(c);
    return c;
  },
  Object: (props = {}, o) => {
    const required = Object.keys(props).filter((k) => !optionalSet.has(props[k]));
    const s = { type: "object", properties: props };
    if (required.length) s.required = required;
    return opts(s, o);
  },
};
export const Static = undefined;
export default { Type };
