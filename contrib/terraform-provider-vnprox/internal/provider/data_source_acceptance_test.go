// SPDX-License-Identifier: Apache-2.0

package provider

// data_source_acceptance_test.go proves this provider's "data sources read
// freely" half of the contract (README.md): both data sources succeed with
// no changeset staged anywhere, against the same real vnproxd +
// cmd/pvemock stack resource_acceptance_test.go uses.

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccTopologyDataSource(t *testing.T) {
	// Must run before testAccProviderConfig() below is evaluated — see
	// resource_acceptance_test.go's identical comment for why this cannot
	// live in PreCheck.
	setupAcceptanceStack(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "vnprox_topology" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.vnprox_topology.test", "id", "topology"),
					resource.TestCheckResourceAttrSet("data.vnprox_topology.test", "generated_at"),
					// testdata/clusters/single-node.yaml has at least one node in
					// scope, so the topology's own node list must be non-empty —
					// a weak but real assertion that this data source actually
					// read something rather than returning an empty shell.
					resource.TestCheckResourceAttrWith("data.vnprox_topology.test", "nodes.#", func(v string) error {
						if v == "0" {
							return fmt.Errorf("expected at least one topology node from the single-node fixture, got 0")
						}
						return nil
					}),
				),
			},
		},
	})
}

func TestAccInventoryDataSource(t *testing.T) {
	setupAcceptanceStack(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// vmbr0 is testdata/clusters/single-node.yaml's pre-existing
				// management bridge — read-only, nothing staged.
				Config: testAccProviderConfig() + `
data "vnprox_inventory" "test" {
  ref = "bridge:pve1:vmbr0"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.vnprox_inventory.test", "ref", "bridge:pve1:vmbr0"),
					resource.TestCheckResourceAttr("data.vnprox_inventory.test", "kind", "bridge"),
					resource.TestCheckResourceAttr("data.vnprox_inventory.test", "node", "pve1"),
					resource.TestCheckResourceAttrSet("data.vnprox_inventory.test", "fields_json"),
				),
			},
		},
	})
}
