// Flat config with no plugin imports, so it works with a bare `eslint` install.
const nodeGlobals = Object.fromEntries(
  [
    "process",
    "console",
    "Buffer",
    "setTimeout",
    "clearTimeout",
    "setInterval",
    "clearInterval",
    "URL",
    "AbortController",
    "TextDecoder",
    "TextEncoder",
    "structuredClone",
    "queueMicrotask",
    "Proxy",
    "Bun",
  ].map((n) => [n, "readonly"]),
);

export default [
  { ignores: ["node_modules/", "bin/", "tui-bubbletea/", "desktop/", "core/app/testdata/", "compat/test/fixture/", "**/*.ts"] },
  {
    files: ["**/*.mjs", "**/*.js"],
    languageOptions: { ecmaVersion: 2023, sourceType: "module", globals: nodeGlobals },
    rules: {
      "no-undef": "error",
      "no-unused-vars": ["error", { args: "none", caughtErrors: "none" }],
      "no-unreachable": "error",
      "no-dupe-keys": "error",
      "no-redeclare": "error",
      "no-self-assign": "error",
      "no-constant-condition": ["error", { checkLoops: false }],
      "no-eval": "error",
      "no-implied-eval": "error",
      "no-new-func": "error",
      "no-throw-literal": "error",
      eqeqeq: ["error", "always", { null: "ignore" }],
      "prefer-const": "error",
      "no-var": "error",
    },
  },
];
