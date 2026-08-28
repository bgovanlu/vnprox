// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// inventoryDataSource reads GET /inventory/{ref} (netRead) for a single
// entity — the second read-only data source proving this provider's "data
// sources read freely" half of the contract (README.md).
type inventoryDataSource struct {
	client *client
}

func newInventoryDataSource() datasource.DataSource { return &inventoryDataSource{} }

func (d *inventoryDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_inventory"
}

type relatedRefModel struct {
	Ref       types.String `tfsdk:"ref"`
	EdgeKind  types.String `tfsdk:"edge_kind"`
	Direction types.String `tfsdk:"direction"`
}

type inventoryDataSourceModel struct {
	Ref         types.String      `tfsdk:"ref"`
	Kind        types.String      `tfsdk:"kind"`
	Node        types.String      `tfsdk:"node"`
	Label       types.String      `tfsdk:"label"`
	FieldsJSON  types.String      `tfsdk:"fields_json"`
	Related     []relatedRefModel `tfsdk:"related"`
	GeneratedAt types.Int64       `tfsdk:"generated_at"`
}

func (d *inventoryDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads one entity's full detail (GET /inventory/{ref}) — e.g. an existing bridge's resolved " +
			"fields, to reference from Terraform configuration without staging anything. Read-only.",
		Attributes: map[string]schema.Attribute{
			"ref": schema.StringAttribute{
				Required:    true,
				Description: "The entity's vnprox Ref triplet, e.g. \"bridge:pve1:vmbr0\" (docs/api.md's IDs convention).",
			},
			"kind":  schema.StringAttribute{Computed: true},
			"node":  schema.StringAttribute{Computed: true},
			"label": schema.StringAttribute{Computed: true},
			"fields_json": schema.StringAttribute{
				Computed:    true,
				Description: "The entity's resolved fields, JSON-encoded (this data source's `fields` map, re-serialized — Terraform's type system has no open map[string]any, so this is exposed as a JSON string for `jsondecode()` in configuration).",
			},
			"generated_at": schema.Int64Attribute{Computed: true},
			"related": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"ref":       schema.StringAttribute{Computed: true},
						"edge_kind": schema.StringAttribute{Computed: true},
						"direction": schema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (d *inventoryDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*vnproxProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("expected *vnproxProviderData, got %T", req.ProviderData))
		return
	}
	d.client = data.client
}

func (d *inventoryDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg inventoryDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	detail, err := d.client.GetInventory(ctx, cfg.Ref.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(fmt.Sprintf("Reading GET /inventory/%s", cfg.Ref.ValueString()), err.Error())
		return
	}

	fieldsJSON, err := json.Marshal(detail.Fields)
	if err != nil {
		resp.Diagnostics.AddError("Encoding fields as JSON", err.Error())
		return
	}

	m := inventoryDataSourceModel{
		Ref: types.StringValue(detail.Ref), Kind: types.StringValue(detail.Kind), Node: types.StringValue(detail.Node),
		Label: types.StringValue(detail.Label), FieldsJSON: types.StringValue(string(fieldsJSON)),
		GeneratedAt: types.Int64Value(detail.GeneratedAt),
	}
	for _, rel := range detail.Related {
		m.Related = append(m.Related, relatedRefModel{
			Ref: types.StringValue(rel.Ref), EdgeKind: types.StringValue(rel.EdgeKind), Direction: types.StringValue(rel.Direction),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
