// SPDX-License-Identifier: Apache-2.0

package api

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/apidoc"
)

// assistant_nopath_test.go is the daemon-side half of T-2808 AC6: "prompt
// content and answers are excluded from logs and support bundles by
// default".
//
// The implementation makes that structural rather than procedural. The
// in-app assistant (web/src/assistant/) talks to the operator's own model
// backend directly from the browser; vnproxd is never in that path. A
// daemon that never receives a prompt cannot log one, and a support bundle
// built from what the daemon has cannot carry one — which is why the bundle
// side of AC6 leans on T-1902's existing declared-entry machinery
// (internal/backup's TestBundle_CarriesNoAssistantTranscript) rather than a
// parallel redaction scan of its own.
//
// The premise that argument rests on is exactly one fact, and it is the one
// asserted here: nothing vnproxd serves accepts assistant prompt content.
//
// It reads apidoc.Operations rather than walking a test-built router on
// purpose. That table is T-2405's gate — a route registered in this package
// with no entry there fails TestOpenAPI_EveryRouteIsDescribed, and an entry
// no route serves fails it in the other direction — so it is the complete
// route surface of the shipped daemon, not the subset a particular test
// happens to wire.
func TestAssistant_NoDaemonRouteAcceptsPromptContent(t *testing.T) {
	// Non-vacuity, both halves: the table is fully populated, and it is the
	// table this test thinks it is.
	if len(apidoc.Operations) < 200 {
		t.Fatalf("apidoc.Operations has %d entries; this scan is not reading the daemon's route table",
			len(apidoc.Operations))
	}
	if _, ok := apidoc.Operations["GET /api/v1/topology"]; !ok {
		t.Fatal("apidoc.Operations does not describe GET /api/v1/topology — its \"no assistant route\" " +
			"result would be meaningless")
	}

	// Substrings a prompt-carrying route would plausibly be spelled with.
	// This is a bar against a future edit, not a claim about today's code.
	forbidden := []string{"assistant", "prompt", "completion", "chat", "llm", "inference"}
	for key := range apidoc.Operations {
		lower := strings.ToLower(key)
		for _, bad := range forbidden {
			if strings.Contains(lower, bad) {
				t.Errorf("the daemon serves %q, which looks like an assistant/prompt route.\n\n"+
					"T-2808's assistant deliberately has NO backend data path: the browser talks to the "+
					"operator's own model backend, so prompts and answers never reach vnproxd and cannot "+
					"reach its logs or a support bundle. A route that takes prompt content changes that "+
					"analysis and needs its own card, its own redaction, and its own bundle declaration.", key)
			}
		}
	}

	// CONTROL: the scan does fire on a prompt-shaped key, so a clean result
	// above means "no such route", not "no such check".
	var caught bool
	for _, bad := range forbidden {
		if strings.Contains(strings.ToLower("POST /api/v1/assistant/ask"), bad) {
			caught = true
		}
	}
	if !caught {
		t.Fatal("the forbidden-substring list does not match \"POST /api/v1/assistant/ask\" — the scan " +
			"above is not testing what it claims to test")
	}
}
