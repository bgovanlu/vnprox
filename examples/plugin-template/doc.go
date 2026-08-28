// Package plugintemplate is a complete, minimal, compiling vnprox plugin: the
// template `vnproxctl plugin scaffold <name>` stamps out (T-3811). It attaches
// to exactly one of the SDK's five v1 extension points — findingProducer, the
// simplest read-only seam — and does nothing else.
//
// # Why this lives under examples/, not a separate repository
//
// This package imports "github.com/bgovanlu/vnprox/internal/plugin" and (in
// its test) "github.com/bgovanlu/vnprox/internal/plugin/plugintest". Both are
// Go "internal" packages: the language enforces that only code rooted under
// github.com/bgovanlu/vnprox (this module) may import them at all — a
// separately cloned/module-versioned "my-vnprox-plugin" repository on GitHub
// literally cannot `go get` and import them, no matter how the import path is
// written. That is not a documentation gap; it is Go's own visibility rule.
//
// Consequently, today, the only way to write an **in-process** plugin (this
// template's transport) is from inside a checkout of this repository — e.g.
// this directory, or a fork. `vnproxctl plugin scaffold` reflects that
// honestly: it writes its output as a package meant to be built from inside a
// vnprox checkout (`go build ./<dir>/...` from the repo root), not as the seed
// of an independently cloneable module. See docs/plugin-development.md's
// "In-process vs. out-of-process, and what that means for where your code
// lives" section for the out-of-process alternative, which has no such
// restriction because it never imports vnprox's Go code at all — it speaks
// internal/plugin/procshim's documented wire protocol (wire.proto) directly,
// over its own stdio, in any language.
//
// # The stage-only boundary
//
// Nothing in this package can apply a network change. A FindingProducer is
// read-only by construction — plugin.FindingProducer's Produce method takes a
// context and returns findings, full stop; there is no Stager, no
// change-engine handle, reachable from this interface at all. See
// docs/plugin-development.md and internal/plugin/doc.go for the SDK-wide
// safety boundary this template inherits without doing anything to earn it.
package plugintemplate

import "embed"

// Files is the exact source this package builds from, embedded so
// `vnproxctl plugin scaffold` (cmd/vnproxctl/plugincmd.go) can stamp out this
// same, tested content into a new directory — not a hand-maintained second
// copy that can drift from what this package actually builds and tests as.
// Substitution (this-package's identifiers -> the caller's chosen name) is
// done by the scaffold command on these exact bytes; nothing here is a
// template-syntax file, so `go build ./examples/plugin-template/...` and
// `go test ./examples/plugin-template/...` exercise the literal content the
// scaffold command later copies.
//
//go:embed manifest.go producer.go producer_test.go README.md
var Files embed.FS
