// ESLint flat config for vnprox. `web/` is presently the only linted
// JS/TypeScript code in the repo (everything else is Go, linted by
// golangci-lint); this is the single config file, found by `make lint`'s
// `cd web && npx eslint .` via ESLint's upward config-file lookup from
// web/ to the repo root. T-005 replaces T-001's placeholder here rather
// than adding a second, competing web/eslint.config.js.
import js from "@eslint/js";
import globals from "globals";
import tseslint from "typescript-eslint";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";

const reactRefreshPlugin = reactRefresh.default ?? reactRefresh;

export default tseslint.config(
  {
    ignores: [
      "web/dist/**",
      "web/node_modules/**",
      "web/coverage/**",
      "dist/**",
      "node_modules/**",
      "bin/**",
    ],
  },
  {
    files: ["src/**/*.{ts,tsx}", "perf/**/*.ts", "vite.config.ts", "e2e/**/*.ts", "playwright.config.ts", "playwright.visual.config.ts"],
    extends: [
      js.configs.recommended,
      ...tseslint.configs.strictTypeChecked,
      ...tseslint.configs.stylisticTypeChecked,
    ],
    languageOptions: {
      ecmaVersion: 2022,
      sourceType: "module",
      globals: { ...globals.browser, ...globals.es2022 },
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefreshPlugin,
    },
    rules: {
      // Only the two long-stable react-hooks rules; v7 ships a much
      // larger "react compiler readiness" ruleset (static-components,
      // purity, immutability, ...) tuned for codebases adopting the
      // React Compiler, which is out of scope here and prone to false
      // positives on ordinary hand-written components.
      "react-hooks/rules-of-hooks": "error",
      "react-hooks/exhaustive-deps": "warn",
      "react-refresh/only-export-components": ["warn", { allowConstantExport: true }],
      // noUnusedLocals/noUnusedParameters in tsconfig already enforce
      // this at the type-checker level; the underscore-prefix escape
      // hatch is still useful for intentionally-unused destructured
      // values, so keep the ESLint rule too (it's not otherwise on by
      // default in the type-checked configs).
      "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_", varsIgnorePattern: "^_" }],
    },
  },
  {
    // vite.config.ts, perf/ (T-2506's budget reader, which reads
    // perf/budgets.json off disk) and the Playwright layer run under Node,
    // not the browser (e2e specs' page.evaluate callbacks execute in the
    // browser, but the spec files themselves are Node programs).
    files: ["vite.config.ts", "perf/**/*.ts", "e2e/**/*.ts", "playwright.config.ts"],
    languageOptions: {
      globals: globals.node,
    },
  },
);
