// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// topologyDataSource reads GET /topology (netRead) — a straight read, no
// staging involved. Every data source in this provider reads freely; only
// resources (resource_bridge.go, resource_vlan.go) touch the change engine,
// and even then only through Create/Validate — see README.md.
type topologyDataSource struct {
	client *client
}

func newTopologyDataSource() datasource.DataSource { return &topologyDataSource{} }

func (d *topologyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_topology"
}

type topologyNodeModel struct {
	ID        types.String `tfsdk:"id"`
	Kind      types.String `tfsdk:"kind"`
	Label     types.String `tfsdk:"label"`
	Layer     types.String `tfsdk:"layer"`
	NodeGroup types.String `tfsdk:"node_group"`
	Status    types.String `tfsdk:"status"`
}

type topologyEdgeModel struct {
	From types.String `tfsdk:"from"`
	To   types.String `tfsdk:"to"`
	Kind types.String `tfsdk:"kind"`
}

type topologyDataSourceModel struct {
	ID          types.String        `tfsdk:"id"`
	Nodes       []topologyNodeModel `tfsdk:"nodes"`
	Edges       []topologyEdgeModel `tfsdk:"edges"`
	GeneratedAt types.Int64         `tfsdk:"generated_at"`
}

func (d *topologyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads vnprox's full projected topology (GET /topology) — every node and edge the cluster " +
			"map renders, for referencing existing infrastructure from Terraform configuration (e.g. picking a " +
			"physical NIC's node before staging a bridge on it). Read-only; this data source never stages anything.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Fixed value \"topology\" (this data source has no configurable identity of its own).",
			},
			"generated_at": schema.Int64Attribute{
				Computed:    true,
				Description: "Unix seconds this topology snapshot was generated at.",
			},
			"nodes": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":         schema.StringAttribute{Computed: true},
						"kind":       schema.StringAttribute{Computed: true},
						"label":      schema.StringAttribute{Computed: true},
						"layer":      schema.StringAttribute{Computed: true},
						"node_group": schema.StringAttribute{Computed: true},
						"status":     schema.StringAttribute{Computed: true},
					},
				},
			},
			"edges": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"from": schema.StringAttribute{Computed: true},
						"to":   schema.StringAttribute{Computed: true},
						"kind": schema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (d *topologyDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *topologyDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	topo, err := d.client.GetTopology(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading GET /topology", err.Error())
		return
	}

	m := topologyDataSourceModel{
		ID:          types.StringValue("topology"),
		GeneratedAt: types.Int64Value(topo.GeneratedAt),
	}
	for _, n := range topo.Nodes {
		m.Nodes = append(m.Nodes, topologyNodeModel{
			ID: types.StringValue(n.ID), Kind: types.StringValue(n.Kind), Label: types.StringValue(n.Label),
			Layer: types.StringValue(n.Layer), NodeGroup: types.StringValue(n.NodeGroup), Status: types.StringValue(n.Status),
		})
	}
	for _, e := range topo.Edges {
		m.Edges = append(m.Edges, topologyEdgeModel{
			From: types.StringValue(e.From), To: types.StringValue(e.To), Kind: types.StringValue(e.Kind),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
