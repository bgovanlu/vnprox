// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// vlanResource stages VLAN sub-interface create/update/delete ops as
// changeset drafts. Same stage-only contract as bridgeResource
// (resource_bridge.go) — see README.md's "The stage-only contract" section
// before reading Create/Update/Delete below. OVS int-port VLANs
// (VlanCreateParams.OVS/Trunks) are deliberately out of scope for this
// provider's first cut, per this card's "prove the contract rather than
// breadth" instruction; a plain 802.1q sub-interface is the whole surface
// here.
type vlanResource struct {
	client *client
}

func newVlanResource() resource.Resource { return &vlanResource{} }

func (r *vlanResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vlan"
}

type vlanModel struct {
	ID              types.String `tfsdk:"id"`
	Node            types.String `tfsdk:"node"`
	Name            types.String `tfsdk:"name"`
	Parent          types.String `tfsdk:"parent"`
	Gateway         types.String `tfsdk:"gateway"`
	Addresses       types.List   `tfsdk:"addresses"`
	Vid             types.Int64  `tfsdk:"vid"`
	MTU             types.Int64  `tfsdk:"mtu"`
	ChangesetID     types.String `tfsdk:"changeset_id"`
	ChangesetStatus types.String `tfsdk:"changeset_status"`
	LiveExists      types.Bool   `tfsdk:"live_exists"`
}

func (r *vlanResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Stages a plain 802.1q VLAN sub-interface (vlan.create/update/delete op) as a draft, validated " +
			"changeset. A `terraform apply` on this resource NEVER creates a live VLAN sub-interface — it stages a " +
			"changeset and stops. See the provider README's \"The stage-only contract\" section.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The VLAN sub-interface's vnprox Ref triplet (\"vlan:<node>:<name>\").",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"node": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "The sub-interface's name, e.g. \"vmbr0.20\".",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"parent": schema.StringAttribute{
				Required:    true,
				Description: "The parent interface's name (change.VlanCreateParams.Parent). Re-parenting is out of scope (RequiresReplace).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"vid": schema.Int64Attribute{
				Required:    true,
				Description: "The VLAN id (change.VlanCreateParams.Vid). Changing it is a different interface identity (RequiresReplace).",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"gateway": schema.StringAttribute{
				Optional:    true,
				Description: "Rendered as the stanza's `gateway` option (change.VlanCreateParams.Gateway) — for a dedicated management VLAN.",
			},
			"addresses": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
			},
			"mtu": schema.Int64Attribute{
				Optional: true,
			},
			"changeset_id": schema.StringAttribute{
				Computed:    true,
				Description: "The id of the changeset this resource most recently staged.",
			},
			"changeset_status": schema.StringAttribute{
				Computed:    true,
				Description: "The staged changeset's status as of the last Terraform read. Never \"applied\" as a result of anything this provider does.",
			},
			"live_exists": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether GET /inventory/{ref} currently resolves for this VLAN sub-interface (informational only).",
			},
		},
	}
}

func (r *vlanResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func vlanRef(node, name string) string {
	return "vlan:" + node + ":" + name
}

// vlanCreateParams mirrors change.VlanCreateParams's wire shape
// (internal/change/params_vlan.go in the main module), OVS/Trunks omitted
// per this file's doc comment.
type vlanCreateParams struct {
	Parent    string   `json:"parent"`
	Gateway   string   `json:"gateway,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
	Vid       int64    `json:"vid"`
	MTU       int64    `json:"mtu,omitempty"`
}

// vlanUpdateParams mirrors change.VlanUpdateParams's wire shape (only
// Addresses/MTU are editable post-create in the main module — Parent/Vid
// are RequiresReplace here for exactly that reason).
type vlanUpdateParams struct {
	Addresses *[]string `json:"addresses,omitempty"`
	MTU       *int64    `json:"mtu,omitempty"`
}

func (m *vlanModel) addressList(ctx context.Context) []string {
	if m.Addresses.IsNull() || m.Addresses.IsUnknown() {
		return nil
	}
	var out []string
	m.Addresses.ElementsAs(ctx, &out, false)
	return out
}

func (m *vlanModel) createOp(ctx context.Context) (op, error) {
	params := vlanCreateParams{
		Parent: m.Parent.ValueString(), Gateway: m.Gateway.ValueString(),
		Addresses: m.addressList(ctx), Vid: m.Vid.ValueInt64(), MTU: m.MTU.ValueInt64(),
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return op{}, fmt.Errorf("encoding vlan.create params: %w", err)
	}
	return op{Op: "vlan.create", Target: vlanRef(m.Node.ValueString(), m.Name.ValueString()), Params: raw}, nil
}

func (m *vlanModel) updateOp(ctx context.Context) (op, error) {
	addrs := m.addressList(ctx)
	mtu := m.MTU.ValueInt64()
	params := vlanUpdateParams{Addresses: &addrs, MTU: &mtu}
	raw, err := json.Marshal(params)
	if err != nil {
		return op{}, fmt.Errorf("encoding vlan.update params: %w", err)
	}
	return op{Op: "vlan.update", Target: vlanRef(m.Node.ValueString(), m.Name.ValueString()), Params: raw}, nil
}

func (m *vlanModel) deleteOp() op {
	return op{Op: "vlan.delete", Target: vlanRef(m.Node.ValueString(), m.Name.ValueString()), Params: json.RawMessage("{}")}
}

func (m *vlanModel) applyChangesetResult(cs changeset) (warnings []string) {
	m.ID = types.StringValue(vlanRef(m.Node.ValueString(), m.Name.ValueString()))
	m.ChangesetID = types.StringValue(cs.ID)
	m.ChangesetStatus = types.StringValue(cs.Status)
	for _, f := range cs.Findings {
		if f.Severity == "error" {
			warnings = append(warnings, fmt.Sprintf("[%s] %s", f.Code, f.Message))
		}
	}
	return warnings
}

func (r *vlanResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m vlanModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}

	o, err := m.createOp(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Encoding vlan.create op", err.Error())
		return
	}
	title := fmt.Sprintf("terraform: create vlan %s on %s", m.Name.ValueString(), m.Node.ValueString())
	cs, err := r.client.CreateChangeset(ctx, title, []op{o})
	if err != nil {
		resp.Diagnostics.AddError("Staging vlan.create changeset", err.Error())
		return
	}
	if validated, verr := r.client.ValidateChangeset(ctx, cs.ID); verr == nil {
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

func (r *vlanResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m vlanModel
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

func (r *vlanResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state vlanModel
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
		o, oerr := plan.createOp(ctx)
		if oerr != nil {
			resp.Diagnostics.AddError("Encoding vlan.create op", oerr.Error())
			return
		}
		updated, uerr := r.client.UpdateChangesetOps(ctx, state.ChangesetID.ValueString(), []op{o})
		if uerr != nil {
			resp.Diagnostics.AddError("Updating staged changeset ops", uerr.Error())
			return
		}
		if validated, verr := r.client.ValidateChangeset(ctx, updated.ID); verr == nil {
			cs = validated
		} else {
			resp.Diagnostics.AddWarning("Validating updated changeset", verr.Error())
			cs = updated
		}
	} else {
		// See resource_bridge.go's Update for the full rationale: a
		// changeset a human already moved past draft/validated is never
		// mutated further — a fresh changeset is staged instead.
		o, oerr := plan.updateOp(ctx)
		if oerr != nil {
			resp.Diagnostics.AddError("Encoding vlan.update op", oerr.Error())
			return
		}
		title := fmt.Sprintf("terraform: update vlan %s on %s", plan.Name.ValueString(), plan.Node.ValueString())
		created, cerr := r.client.CreateChangeset(ctx, title, []op{o})
		if cerr != nil {
			resp.Diagnostics.AddError("Staging vlan.update changeset", cerr.Error())
			return
		}
		if validated, verr := r.client.ValidateChangeset(ctx, created.ID); verr == nil {
			cs = validated
		} else {
			resp.Diagnostics.AddWarning("Validating staged changeset", verr.Error())
			cs = created
		}
	}

	warnings := plan.applyChangesetResult(cs)
	exists, _ := r.client.EntityExists(ctx, plan.ID.ValueString())
	plan.LiveExists = types.BoolValue(exists)
	stageBlockingFindingsWarning(&resp.Diagnostics, cs.ID, warnings)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vlanResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m vlanModel
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
		if derr := r.client.DeleteChangeset(ctx, m.ChangesetID.ValueString()); derr != nil {
			resp.Diagnostics.AddError("Discarding staged changeset", derr.Error())
			return
		}
	default:
		title := fmt.Sprintf("terraform: delete vlan %s on %s", m.Name.ValueString(), m.Node.ValueString())
		created, cerr := r.client.CreateChangeset(ctx, title, []op{m.deleteOp()})
		if cerr != nil {
			resp.Diagnostics.AddError("Staging vlan.delete changeset", cerr.Error())
			return
		}
		if _, verr := r.client.ValidateChangeset(ctx, created.ID); verr != nil {
			resp.Diagnostics.AddWarning("Validating staged deletion changeset", verr.Error())
		}
		resp.Diagnostics.AddWarning(
			"VLAN deletion staged, not applied",
			fmt.Sprintf(
				"The VLAN's original changeset (%s) was already applied, so `terraform destroy` staged a NEW "+
					"changeset (%s) with a vlan.delete op instead. The live sub-interface still exists until a human "+
					"reviews and applies changeset %s inside vnprox.",
				m.ChangesetID.ValueString(), created.ID, created.ID,
			),
		)
	}
}

func (r *vlanResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
