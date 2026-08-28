// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// bridgeResource stages Linux bridge create/update/delete ops as changeset
// drafts. See this package's doc comment (provider.go) and README.md's
// "The stage-only contract" section before reading Create/Update/Delete
// below: none of them ever reach POST /changesets/{id}/apply.
type bridgeResource struct {
	client *client
}

func newBridgeResource() resource.Resource { return &bridgeResource{} }

func (r *bridgeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bridge"
}

// bridgeModel is this resource's schema-bound Go type.
type bridgeModel struct {
	ID              types.String `tfsdk:"id"`
	Node            types.String `tfsdk:"node"`
	Name            types.String `tfsdk:"name"`
	Gateway         types.String `tfsdk:"gateway"`
	Comments        types.String `tfsdk:"comments"`
	Addresses       types.List   `tfsdk:"addresses"`
	MTU             types.Int64  `tfsdk:"mtu"`
	VlanAware       types.Bool   `tfsdk:"vlan_aware"`
	STP             types.Bool   `tfsdk:"stp"`
	ChangesetID     types.String `tfsdk:"changeset_id"`
	ChangesetStatus types.String `tfsdk:"changeset_status"`
	LiveExists      types.Bool   `tfsdk:"live_exists"`
}

func (r *bridgeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Stages a Linux bridge (bridge.create/update/delete op) as a draft, validated changeset. " +
			"A `terraform apply` on this resource NEVER creates a live bridge — it stages a changeset and stops. " +
			"See the provider README's \"The stage-only contract\" section, and go review the changeset it prints " +
			"in vnprox before it does anything.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The bridge's vnprox Ref triplet (\"bridge:<node>:<name>\"), used as this resource's import id.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"node": schema.StringAttribute{
				Required:    true,
				Description: "The PVE node this bridge is staged on.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "The bridge's interface name, e.g. \"vmbr99\".",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"gateway": schema.StringAttribute{
				Optional:    true,
				Description: "Default gateway rendered as the stanza's `gateway` option (change.BridgeCreateParams.Gateway).",
			},
			"comments": schema.StringAttribute{
				Optional: true,
			},
			"addresses": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "CIDR addresses on this bridge.",
			},
			"mtu": schema.Int64Attribute{
				Optional: true,
			},
			"vlan_aware": schema.BoolAttribute{
				Optional: true,
			},
			"stp": schema.BoolAttribute{
				Optional: true,
			},
			"changeset_id": schema.StringAttribute{
				Computed:    true,
				Description: "The id of the changeset this resource most recently staged. Review it at your vnprox instance's Changesets screen.",
			},
			"changeset_status": schema.StringAttribute{
				Computed: true,
				Description: "The staged changeset's status as of the last Terraform read (\"draft\", \"validated\", " +
					"or whatever it has progressed to via a HUMAN review action outside Terraform — this provider never " +
					"advances it past \"validated\" itself). This is never \"applied\" as a result of anything this " +
					"provider does.",
			},
			"live_exists": schema.BoolAttribute{
				Computed: true,
				Description: "Whether GET /inventory/{ref} currently resolves for this bridge — i.e. whether a human " +
					"has actually applied the staged changeset yet. This is informational only; Terraform's own plan/apply " +
					"cycle for this resource is driven by the changeset, not by this value.",
			},
		},
	}
}

func (r *bridgeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*vnproxProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("expected *vnproxProviderData, got %T", req.ProviderData))
		return
	}
	r.client = data.client
}

func bridgeRef(node, name string) string {
	return "bridge:" + node + ":" + name
}

// bridgeCreateParams mirrors change.BridgeCreateParams's wire shape
// (internal/change/params_bridge.go in the main module) field-for-field —
// see client.go's package doc comment for why this is reimplemented rather
// than imported.
type bridgeCreateParams struct {
	Gateway   string   `json:"gateway,omitempty"`
	Comments  string   `json:"comments,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
	MTU       int64    `json:"mtu,omitempty"`
	VlanAware bool     `json:"vlanAware,omitempty"`
	STP       bool     `json:"stp,omitempty"`
}

// bridgeUpdateParams mirrors change.BridgeUpdateParams's wire shape. Every
// field is a pointer exactly like its main-module counterpart, so a value
// this resource doesn't manage never round-trips as an accidental clear.
type bridgeUpdateParams struct {
	Gateway   *string   `json:"gateway,omitempty"`
	Comments  *string   `json:"comments,omitempty"`
	Addresses *[]string `json:"addresses,omitempty"`
	MTU       *int64    `json:"mtu,omitempty"`
	VlanAware *bool     `json:"vlanAware,omitempty"`
	STP       *bool     `json:"stp,omitempty"`
}

func (m *bridgeModel) addressList(ctx context.Context) []string {
	if m.Addresses.IsNull() || m.Addresses.IsUnknown() {
		return nil
	}
	var out []string
	m.Addresses.ElementsAs(ctx, &out, false)
	return out
}

func (m *bridgeModel) createOp(ctx context.Context) (op, error) {
	params := bridgeCreateParams{
		Gateway:   m.Gateway.ValueString(),
		Comments:  m.Comments.ValueString(),
		Addresses: m.addressList(ctx),
		MTU:       m.MTU.ValueInt64(),
		VlanAware: m.VlanAware.ValueBool(),
		STP:       m.STP.ValueBool(),
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return op{}, fmt.Errorf("encoding bridge.create params: %w", err)
	}
	return op{Op: "bridge.create", Target: bridgeRef(m.Node.ValueString(), m.Name.ValueString()), Params: raw}, nil
}

func (m *bridgeModel) updateOp(ctx context.Context) (op, error) {
	addrs := m.addressList(ctx)
	gw := m.Gateway.ValueString()
	comments := m.Comments.ValueString()
	mtu := m.MTU.ValueInt64()
	vlanAware := m.VlanAware.ValueBool()
	stp := m.STP.ValueBool()
	params := bridgeUpdateParams{
		Gateway: &gw, Comments: &comments, Addresses: &addrs,
		MTU: &mtu, VlanAware: &vlanAware, STP: &stp,
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return op{}, fmt.Errorf("encoding bridge.update params: %w", err)
	}
	return op{Op: "bridge.update", Target: bridgeRef(m.Node.ValueString(), m.Name.ValueString()), Params: raw}, nil
}

func (m *bridgeModel) deleteOp() op {
	return op{Op: "bridge.delete", Target: bridgeRef(m.Node.ValueString(), m.Name.ValueString()), Params: json.RawMessage("{}")}
}

// applyChangesetResult folds a changeset response back into m's computed
// attributes (id/changeset_id/changeset_status) and returns a blocking-
// findings warning message, if any — shared by Create/Update so both
// surface the same "staged; here's what to look at" shape.
func (m *bridgeModel) applyChangesetResult(cs changeset) (warnings []string) {
	m.ID = types.StringValue(bridgeRef(m.Node.ValueString(), m.Name.ValueString()))
	m.ChangesetID = types.StringValue(cs.ID)
	m.ChangesetStatus = types.StringValue(cs.Status)
	for _, f := range cs.Findings {
		if f.Severity == "error" {
			warnings = append(warnings, fmt.Sprintf("[%s] %s", f.Code, f.Message))
		}
	}
	return warnings
}

func stageBlockingFindingsWarning(resp interface {
	AddWarning(summary, detail string)
}, changesetID string, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	detail := fmt.Sprintf(
		"Changeset %s was staged but did not validate clean; it stays in \"draft\" until these are resolved and it is "+
			"revalidated inside vnprox (this provider never revalidates on your behalf outside terraform apply/refresh):\n- %s",
		changesetID, joinLines(warnings),
	)
	resp.AddWarning("vnprox changeset has blocking validation findings", detail)
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n- "
		}
		out += l
	}
	return out
}

func (r *bridgeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m bridgeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}

	o, err := m.createOp(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Encoding bridge.create op", err.Error())
		return
	}

	title := fmt.Sprintf("terraform: create bridge %s on %s", m.Name.ValueString(), m.Node.ValueString())
	cs, err := r.client.CreateChangeset(ctx, title, []op{o})
	if err != nil {
		resp.Diagnostics.AddError("Staging bridge.create changeset", err.Error())
		return
	}
	// Validate is the second half of the stage-only Create/Validate pair —
	// see this file's and client.go's doc comments. A validate failure
	// (network/daemon error, as opposed to blocking findings, which are a
	// normal validated-with-findings response) is reported but the
	// already-staged draft is kept in state either way: Create succeeded at
	// staging, which is this resource's actual contract.
	validated, verr := r.client.ValidateChangeset(ctx, cs.ID)
	if verr == nil {
		cs = validated
	} else {
		resp.Diagnostics.AddWarning("Validating staged changeset", verr.Error())
	}

	warnings := m.applyChangesetResult(cs)
	exists, _ := r.client.EntityExists(ctx, m.ID.ValueString())
	m.LiveExists = types.BoolValue(exists)
	stageBlockingFindingsWarning(&resp.Diagnostics, cs.ID, warnings)

	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bridgeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m bridgeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cs, err := r.client.GetChangeset(ctx, m.ChangesetID.ValueString())
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading changeset", err.Error())
		return
	}
	m.ChangesetStatus = types.StringValue(cs.Status)

	exists, err := r.client.EntityExists(ctx, m.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddWarning("Checking live inventory", err.Error())
	} else {
		m.LiveExists = types.BoolValue(exists)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// changesetEditable reports whether status is still draft/validated —
// mirrors change.Changeset.Editable() in the main module (draft CRUD's own
// eligibility check), reimplemented here for the same module-boundary
// reason every wire type in this package is.
func changesetEditable(status string) bool {
	return status == "draft" || status == "validated"
}

func (r *bridgeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state bridgeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.client.GetChangeset(ctx, state.ChangesetID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading current changeset before update", err.Error())
		return
	}

	var cs changeset
	if changesetEditable(current.Status) {
		// The original staged changeset is still a draft/validated — revise
		// its own ops in place (PUT /changesets/{id}), matching this
		// provider's Create shape exactly.
		o, oerr := plan.createOp(ctx)
		if oerr != nil {
			resp.Diagnostics.AddError("Encoding bridge.create op", oerr.Error())
			return
		}
		updated, uerr := r.client.UpdateChangesetOps(ctx, state.ChangesetID.ValueString(), []op{o})
		if uerr != nil {
			resp.Diagnostics.AddError("Updating staged changeset ops", uerr.Error())
			return
		}
		validated, verr := r.client.ValidateChangeset(ctx, updated.ID)
		if verr != nil {
			resp.Diagnostics.AddWarning("Validating updated changeset", verr.Error())
			cs = updated
		} else {
			cs = validated
		}
	} else {
		// A human already moved the original changeset past draft/validated
		// (applying it, or further along) — see README.md's "What happens
		// after a human applies" section. This provider does not, and must
		// not, try to mutate that changeset; it stages a NEW one carrying a
		// bridge.update op instead, exactly the way a second hand-authored
		// edit through the vnprox UI would after the first was applied.
		o, oerr := plan.updateOp(ctx)
		if oerr != nil {
			resp.Diagnostics.AddError("Encoding bridge.update op", oerr.Error())
			return
		}
		title := fmt.Sprintf("terraform: update bridge %s on %s", plan.Name.ValueString(), plan.Node.ValueString())
		created, cerr := r.client.CreateChangeset(ctx, title, []op{o})
		if cerr != nil {
			resp.Diagnostics.AddError("Staging bridge.update changeset", cerr.Error())
			return
		}
		validated, verr := r.client.ValidateChangeset(ctx, created.ID)
		if verr != nil {
			resp.Diagnostics.AddWarning("Validating staged changeset", verr.Error())
			cs = created
		} else {
			cs = validated
		}
	}

	warnings := plan.applyChangesetResult(cs)
	exists, _ := r.client.EntityExists(ctx, plan.ID.ValueString())
	plan.LiveExists = types.BoolValue(exists)
	stageBlockingFindingsWarning(&resp.Diagnostics, cs.ID, warnings)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bridgeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m bridgeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.client.GetChangeset(ctx, m.ChangesetID.ValueString())
	if err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Reading current changeset before delete", err.Error())
		return
	}

	switch {
	case err != nil: // 404: nothing left to discard
	case changesetEditable(current.Status):
		// Still a draft/validated changeset that never became live —
		// discard it outright (DELETE /changesets/{id}), the same action a
		// human clicking "Discard draft" in vnprox would take.
		if derr := r.client.DeleteChangeset(ctx, m.ChangesetID.ValueString()); derr != nil {
			resp.Diagnostics.AddError("Discarding staged changeset", derr.Error())
			return
		}
	default:
		// It was applied (or is applying/beyond) — stage a NEW changeset
		// carrying a bridge.delete op and stop. `terraform destroy` still
		// removes this resource from Terraform state (Terraform's own
		// contract for Delete), but the live bridge is untouched until a
		// human reviews and applies the deletion changeset this stages.
		// This is the same "Delete stages, never applies" asymmetry
		// README.md's "The stage-only contract" section calls out.
		title := fmt.Sprintf("terraform: delete bridge %s on %s", m.Name.ValueString(), m.Node.ValueString())
		created, cerr := r.client.CreateChangeset(ctx, title, []op{m.deleteOp()})
		if cerr != nil {
			resp.Diagnostics.AddError("Staging bridge.delete changeset", cerr.Error())
			return
		}
		if _, verr := r.client.ValidateChangeset(ctx, created.ID); verr != nil {
			resp.Diagnostics.AddWarning("Validating staged deletion changeset", verr.Error())
		}
		resp.Diagnostics.AddWarning(
			"Bridge deletion staged, not applied",
			fmt.Sprintf(
				"The bridge's original changeset (%s) was already applied, so `terraform destroy` staged a NEW "+
					"changeset (%s) with a bridge.delete op instead of touching the applied one. The live bridge "+
					"still exists until a human reviews and applies changeset %s inside vnprox.",
				m.ChangesetID.ValueString(), created.ID, created.ID,
			),
		)
	}
}

func (r *bridgeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import by the bridge's own Ref ("bridge:<node>:<name>") — the
	// changeset half of state is populated on the next Read only if a
	// changeset_id is separately known; a bare import has none, so
	// changeset_id/changeset_status start empty and the next `terraform
	// plan` will show this resource as needing to be created (staged) —
	// there is no changeset to import FROM, only a live entity, and this
	// provider's Read never invents one.
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
