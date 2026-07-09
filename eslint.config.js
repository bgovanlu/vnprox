// This repo's only linted JS/TypeScript code lives in web/ (everything
// else is Go, linted separately by golangci-lint). ESLint's flat-config
// plugin imports are bare specifiers resolved relative to the *defining*
// file's own node_modules ancestry, which only exists under web/ (there
// is no root-level node_modules) — so the real, substantive config lives
// in web/eslint.config.js, co-located with the node_modules it needs.
// This file re-exports it as a thin pointer so there is exactly one
// config to maintain, reachable whether ESLint is invoked from the repo
// root or (as `make lint` does) from web/ itself.
export { default } from "./web/eslint.config.js";
