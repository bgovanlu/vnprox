// SPDX-License-Identifier: Apache-2.0

package provider

// resource_acceptance_test.go is T-4001's acceptance-criterion-1 and
// acceptance-criterion-3 proof, table-driven per resource type (matching
// this repo's existing table-driven test convention, and this card's own
// instruction): for each of this provider's two resources, `terraform
// apply` against a REAL vnproxd (harness_test.go's cmd/pvemock + cmd/
// vnproxd subprocess pair) must produce a changeset visible at
// GET /changesets/{id} whose status is "draft" or "validated" —
// NEVER "applied" — carrying exactly the op this resource's Create staged.
//
// This is the load-bearing check for the whole card: it proves, against
// the real production HTTP handlers (not a hand-rolled fake), that this
// provider's resources stop exactly where README.md says they stop.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

type resourceAcceptanceCase struct {
	name         string
	resourceType string
	resourceName string
	config       string
	wantOp       string
	wantTarget   string
}

func resourceAcceptanceCases() []resourceAcceptanceCase {
	return []resourceAcceptanceCase{
		{
			name:         "bridge",
			resourceType: "vnprox_bridge",
			resourceName: "test",
			config: `
resource "vnprox_bridge" "test" {
  node       = "pve1"
  name       = "vmbr99"
  gateway    = "10.99.0.1"
  addresses  = ["10.99.0.2/24"]
  mtu        = 1500
  vlan_aware = true
}
`,
			wantOp:     "bridge.create",
			wantTarget: "bridge:pve1:vmbr99",
		},
		{
			name:         "vlan",
			resourceType: "vnprox_vlan",
			resourceName: "test",
			config: `
resource "vnprox_vlan" "test" {
  node      = "pve1"
  name      = "vmbr0.42"
  parent    = "vmbr0"
  vid       = 42
  addresses = ["10.42.0.2/24"]
}
`,
			wantOp:     "vlan.create",
			wantTarget: "vlan:pve1:vmbr0.42",
		},
	}
}

// TestAccResources_StageOnly runs every case in resourceAcceptanceCases
// through a single `terraform apply` and asserts the resulting changeset is
// staged/validated, never applied — see this file's doc comment.
func TestAccResources_StageOnly(t *testing.T) {
	for _, tc := range resourceAcceptanceCases() {
		t.Run(tc.name, func(t *testing.T) {
			// setupAcceptanceStack must run — and testAccBaseURL/testAccToken
			// must be populated — BEFORE testAccProviderConfig() is called
			// below: resource.TestCase's Steps[].Config is an ordinary string
			// field, evaluated when this struct literal is built, which
			// happens before resource.Test ever invokes PreCheck. Putting the
			// stack setup in PreCheck instead would silently bake an empty
			// (or, worse, a PREVIOUS subtest's stale) base_url/token into the
			// rendered config — exactly the bug this comment replaced.
			setupAcceptanceStack(t)

			resourceAddr := fmt.Sprintf("%s.%s", tc.resourceType, tc.resourceName)

			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: testAccProviderConfig() + tc.config,
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttrSet(resourceAddr, "changeset_id"),
							resource.TestCheckResourceAttr(resourceAddr, "id", tc.wantTarget),
							checkChangesetStagedNotApplied(t, resourceAddr, tc.wantOp, tc.wantTarget),
						),
					},
				},
			})
		})
	}
}

// checkChangesetStagedNotApplied reads the resource's changeset_id out of
// Terraform state, then calls GET /changesets/{id} directly against the
// running vnproxd (bypassing the provider entirely, the way an external
// auditor would) and asserts:
//  1. status is "draft" or "validated" — never "applied", "applying",
//     "awaiting_confirm", "committed", or "rolled_back" (acceptance
//     criterion 1's exact wording).
//  2. the changeset's ops carry exactly the op type/target this resource
//     was supposed to stage.
func checkChangesetStagedNotApplied(t *testing.T, resourceAddr, wantOp, wantTarget string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceAddr]
		if !ok {
			return fmt.Errorf("resource %s not found in state", resourceAddr)
		}
		changesetID := rs.Primary.Attributes["changeset_id"]
		if changesetID == "" {
			return fmt.Errorf("resource %s has no changeset_id", resourceAddr)
		}

		cs, err := fetchChangeset(t, changesetID)
		if err != nil {
			return err
		}

		switch cs.Status {
		case "draft", "validated":
			// exactly acceptance criterion 1's allowed set
		case "applied", "applying", "awaiting_confirm", "committed", "rolled_back":
			return fmt.Errorf("changeset %s has status %q — a terraform apply must NEVER advance a changeset past validated (T-4001 AC1)", changesetID, cs.Status)
		default:
			return fmt.Errorf("changeset %s has unrecognized status %q", changesetID, cs.Status)
		}

		if len(cs.Ops) != 1 {
			return fmt.Errorf("changeset %s has %d ops, want exactly 1", changesetID, len(cs.Ops))
		}
		if cs.Ops[0].Op != wantOp {
			return fmt.Errorf("changeset %s op = %q, want %q", changesetID, cs.Ops[0].Op, wantOp)
		}
		if cs.Ops[0].Target != wantTarget {
			return fmt.Errorf("changeset %s op target = %q, want %q", changesetID, cs.Ops[0].Target, wantTarget)
		}
		return nil
	}
}

// fetchChangeset performs a raw GET /changesets/{id} against the running
// acceptance-stack daemon, independent of this provider's own client.go —
// deliberately a second, from-scratch HTTP call, so this check verifies
// what vnproxd actually did rather than merely what the provider's own
// client believes it did.
func fetchChangeset(t *testing.T, id string) (changeset, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, testAccBaseURL+"/changesets/"+id, nil)
	if err != nil {
		return changeset{}, err
	}
	req.Header.Set("Authorization", "Bearer "+testAccToken)
	resp, err := insecureAcceptanceHTTPClient().Do(req)
	if err != nil {
		return changeset{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return changeset{}, fmt.Errorf("GET /changesets/%s: HTTP %d", id, resp.StatusCode)
	}
	var cs changeset
	if err := json.NewDecoder(resp.Body).Decode(&cs); err != nil {
		return changeset{}, fmt.Errorf("decoding changeset %s: %w", id, err)
	}
	return cs, nil
}
