// SPDX-License-Identifier: Apache-2.0

// Package provider implements terraform-provider-vnprox — a Terraform/
// OpenTofu provider for vnprox (T-4001). Read this repository's README.md
// before touching resource_*.go: every resource's Create/Update maps to
// exactly one staged, validated changeset (POST /changesets +
// POST /changesets/{id}/validate) and NEVER an apply
// (POST /changesets/{id}/apply). `terraform apply` stages a draft/validated
// changeset and stops; a human reviews and applies it inside vnprox. That
// is not a limitation of this provider to work around — it is vnprox's core
// safety guarantee (docs/architecture.md, decision D4: the change engine is
// the sole mutation path), and this provider inherits it exactly the way
// internal/plugin.Stager and internal/mcp.ChangesetStager do in-process.
package provider

import (
	"context"
	"crypto/tls"
	"net/http"
	"os"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// envURL/envToken/envInsecure are the environment-variable fallbacks for
// the provider's own config block, mirroring cmd/vnproxctl/remoteclient.go's
// --url/--token/VNPROX_TOKEN precedent in the main module (a Terraform
// provider has no CLI flags of its own, so these are its equivalent of
// vnproxctl's --url/--token/VNPROX_TOKEN triple).
const (
	envURL      = "VNPROX_URL"
	envToken    = "VNPROX_TOKEN"
	envInsecure = "VNPROX_INSECURE"
)

// vnproxProvider is the top-level provider.Provider implementation.
type vnproxProvider struct {
	// version is set by main.go from the build (or "dev" for a local
	// `go run`), surfaced in the User-Agent header only — it has no
	// behavioral effect.
	version string
}

// New returns a factory function of the shape providerserver.Serve expects.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &vnproxProvider{version: version}
	}
}

func (p *vnproxProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "vnprox"
	resp.Version = p.version
}

// providerModel is the provider config block's schema-bound Go type.
type providerModel struct {
	BaseURL  types.String `tfsdk:"base_url"`
	Token    types.String `tfsdk:"token"`
	Insecure types.Bool   `tfsdk:"insecure"`
	Timeout  types.Int64  `tfsdk:"timeout_seconds"`
}

func (p *vnproxProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Interacts with a vnprox daemon (vnproxd)'s /api/v1 surface. " +
			"IMPORTANT: this provider's resources are stage-only — see the provider README's " +
			"\"The stage-only contract\" section before writing any resource block. " +
			"`terraform apply` never makes a network change live; it stages a changeset for a human to review and apply inside vnprox.",
		Attributes: map[string]schema.Attribute{
			"base_url": schema.StringAttribute{
				Optional: true,
				Description: "vnproxd's /api/v1 base URL, e.g. \"https://pve1:8007/api/v1\". " +
					"Falls back to the " + envURL + " environment variable.",
			},
			"token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "A T-1104 bearer token minted via `POST /tokens` (never a PVE username/password — " +
					"see docs/security.md in the vnprox repo and this provider's README). " +
					"Falls back to the " + envToken + " environment variable.",
			},
			"insecure": schema.BoolAttribute{
				Optional: true,
				Description: "Skip TLS certificate verification. Defaults to false: unlike vnproxctl's dev-convenience " +
					"default, this provider defaults to verifying vnproxd's certificate, since Terraform state and " +
					"CI runs are a worse place to silently trust an unverified endpoint than an interactive CLI is. " +
					"Falls back to the " + envInsecure + " environment variable (\"true\"/\"1\").",
			},
			"timeout_seconds": schema.Int64Attribute{
				Optional:    true,
				Description: "Per-request timeout in seconds. Defaults to 30.",
			},
		},
	}
}

// vnproxProviderData is what Configure hands to every resource/data
// source's Configure method via req.ProviderData.
type vnproxProviderData struct {
	client *client
}

func (p *vnproxProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	baseURL := cfg.BaseURL.ValueString()
	if baseURL == "" {
		baseURL = os.Getenv(envURL)
	}
	if baseURL == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("base_url"),
			"Missing vnprox base_url",
			"Set the provider's base_url attribute or the "+envURL+" environment variable, e.g. \"https://pve1:8007/api/v1\".",
		)
	}

	token := cfg.Token.ValueString()
	if token == "" {
		token = os.Getenv(envToken)
	}
	if token == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("token"),
			"Missing vnprox token",
			"Set the provider's token attribute or the "+envToken+" environment variable to a T-1104 bearer token "+
				"minted via POST /tokens. This provider never authenticates with a PVE username/password.",
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	insecure := cfg.Insecure.ValueBool()
	if cfg.Insecure.IsNull() {
		if v := os.Getenv(envInsecure); v == "true" || v == "1" {
			insecure = true
		}
	}

	timeout := 30 * time.Second
	if !cfg.Timeout.IsNull() {
		timeout = time.Duration(cfg.Timeout.ValueInt64()) * time.Second
	}

	httpClient := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec // opt-out attribute, off by default
		},
	}

	data := &vnproxProviderData{client: newClient(httpClient, baseURL, token)}
	resp.ResourceData = data
	resp.DataSourceData = data
}

func (p *vnproxProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newBridgeResource,
		newVlanResource,
	}
}

func (p *vnproxProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newTopologyDataSource,
		newInventoryDataSource,
	}
}
