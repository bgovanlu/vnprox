// SPDX-License-Identifier: Apache-2.0

// Command terraform-provider-vnprox is a Terraform/OpenTofu provider for
// vnprox (T-4001). Read internal/provider/provider.go's package doc comment
// and this repository's README.md before making any change: the provider's
// resources are stage-only by design (they never advance a changeset past
// POST /changesets/{id}/validate), the same guarantee vnprox's own
// internal/plugin and internal/mcp integration seams enforce in-process.
//
// This is its own Go module (see go.mod) deliberately isolated from
// cmd/vnproxd's and cmd/vnproxctl's build graphs in the main vnprox module —
// terraform-plugin-framework/terraform-plugin-go are a large dependency
// tree that must never end up linked into the daemon or the CLI (the same
// isolation commit 34c11588 gives sigstore-go, kept out of vnproxd and
// scoped to vnproxctl alone; see cmd/vnproxd/tfproviderguard_test.go in the
// main module for the compile-time-adjacent proof).
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/bgovanlu/terraform-provider-vnprox/internal/provider"
)

// version is overridden at build time via
// -ldflags "-X main.version=..." (packaging, once this provider is
// published); "dev" is what a local `go run .` / `go install` reports.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run in debug mode, for attaching a delve/terraform-plugin-debug session")
	flag.Parse()

	opts := providerserver.ServeOpts{
		// Address is the provider's source address Terraform's provider
		// installer resolves — set to this repository's actual path so
		// `terraform init` with a dev_overrides block (README.md's install
		// section) finds it without a registry entry.
		Address: "registry.terraform.io/bgovanlu/vnprox",
		Debug:   debug,
	}

	if err := providerserver.Serve(context.Background(), provider.New(version), opts); err != nil {
		log.Fatal(err)
	}
}
