// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// testAccProtoV6ProviderFactories is the standard terraform-plugin-testing
// wiring: resource.Test drives a real Terraform CLI (found on $PATH; see
// README.md's "Running the acceptance tests" section) against this
// provider's in-process protocol v6 server.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"vnprox": providerserver.NewProtocol6WithError(New("acctest")()),
}
