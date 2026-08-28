//go:build ignore

// SPDX-License-Identifier: Apache-2.0

// Command gen_corpus regenerates the committed pcap sample corpus under
// testdata/captures/. Run from the repo root:
//
//	go run ./internal/capturemock/gen_corpus.go
//
// The output is byte-deterministic (fixed timestamps in GenerateCorpus), so
// re-running it on an unchanged frame set produces no diff.
package main

import (
	"log"

	"github.com/bgovanlu/vnprox/internal/capturemock"
)

func main() {
	if err := capturemock.GenerateCorpus("testdata/captures"); err != nil {
		log.Fatalf("generating capture corpus: %v", err)
	}
	log.Println("wrote testdata/captures/")
}
