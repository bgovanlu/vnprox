package change

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

func qosShapeTarget() inventory.Ref {
	return inventory.Ref{Kind: inventory.KindQosShape, Node: "pve1", ID: "shape1"}
}

// TestValidate_QosShapeCreate_Schema is T-1505 acceptance criterion 2:
// schema validation rejects rateMbit > ceilMbit and non-positive values —
// table test. Every case uses a schema-invalid shape, so ValidateWithSafety
// short-circuits before referential's bridge-existence check ever runs
// (validate.go's documented class ordering), keeping this table purely
// about the schema class.
func TestValidate_QosShapeCreate_Schema(t *testing.T) {
	tests := []struct {
		name   string
		params *QosShapeCreateParams
		want   []wantFinding
	}{
		{
			name:   "ceilMbit below rateMbit is rejected",
			params: &QosShapeCreateParams{Bridge: "vmbr0", RateMbit: 20, CeilMbit: intPtr(10)},
			want:   []wantFinding{{SeverityError, codeQosRateInvalid, "qos-shape:pve1:shape1"}},
		},
		{
			name:   "non-positive rateMbit is rejected",
			params: &QosShapeCreateParams{Bridge: "vmbr0", RateMbit: 0},
			want:   []wantFinding{{SeverityError, codeQosRateInvalid, "qos-shape:pve1:shape1"}},
		},
		{
			name:   "negative rateMbit is rejected",
			params: &QosShapeCreateParams{Bridge: "vmbr0", RateMbit: -5},
			want:   []wantFinding{{SeverityError, codeQosRateInvalid, "qos-shape:pve1:shape1"}},
		},
		{
			name:   "non-positive ceilMbit is rejected even with a positive rate",
			params: &QosShapeCreateParams{Bridge: "vmbr0", RateMbit: 10, CeilMbit: intPtr(0)},
			want:   []wantFinding{{SeverityError, codeQosRateInvalid, "qos-shape:pve1:shape1"}},
		},
		{
			name:   "matchVlan out of range is rejected",
			params: &QosShapeCreateParams{Bridge: "vmbr0", RateMbit: 10, MatchVlan: intPtr(4095)},
			want:   []wantFinding{{SeverityError, codeQosVlanOutOfRange, "qos-shape:pve1:shape1"}},
		},
		{
			name:   "missing bridge is rejected",
			params: &QosShapeCreateParams{RateMbit: 10},
			want:   []wantFinding{{SeverityError, codeRequiredFieldMissing, "qos-shape:pve1:shape1"}},
		},
		{
			name: "positive rate with ceil >= rate, valid vlan and CIDR — clean",
			params: &QosShapeCreateParams{
				Bridge: "vmbr0", RateMbit: 10, CeilMbit: intPtr(20), MatchVlan: intPtr(100), MatchCIDR: "10.10.0.0/24",
			},
			want: nil,
		},
	}

	snap := buildSnapshot(&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0"})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := mkOp(OpQosShapeCreate, qosShapeTarget(), tt.params)
			findings := Validate([]Op{op}, snap)
			assertFindings(t, findings, tt.want)
		})
	}
}

// TestValidate_QosShapeUpdate_Schema covers the partial-patch update op:
// a rate-only update revalidates rate<=ceil against the *new* rate and any
// explicitly-set ceil (it does not know the shape's currently-stored ceil —
// this pure, snapshot-driven validator class never reads app-store state,
// exactly like every other *UpdateParams schema case in this file).
func TestValidate_QosShapeUpdate_Schema(t *testing.T) {
	tests := []struct {
		name   string
		params *QosShapeUpdateParams
		want   []wantFinding
	}{
		{
			name:   "ceilMbit below rateMbit is rejected",
			params: &QosShapeUpdateParams{RateMbit: intPtr(20), CeilMbit: intPtr(10)},
			want:   []wantFinding{{SeverityError, codeQosRateInvalid, "qos-shape:pve1:shape1"}},
		},
		{
			name:   "non-positive rateMbit is rejected",
			params: &QosShapeUpdateParams{RateMbit: intPtr(0)},
			want:   []wantFinding{{SeverityError, codeQosRateInvalid, "qos-shape:pve1:shape1"}},
		},
		{
			name:   "rate-less update with a valid ceil is clean",
			params: &QosShapeUpdateParams{CeilMbit: intPtr(50)},
			want:   nil,
		},
		{
			name:   "rate and ceil both valid",
			params: &QosShapeUpdateParams{RateMbit: intPtr(10), CeilMbit: intPtr(20)},
			want:   nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := mkOp(OpQosShapeUpdate, qosShapeTarget(), tt.params)
			findings := schemaValidate([]Op{op})
			assertFindings(t, findings, tt.want)
		})
	}
}

// TestValidate_QosShapeCreate_ReferentialBridge is the referential half
// (validator class 2): a shape's Bridge must name a currently known bridge
// on the target's node.
func TestValidate_QosShapeCreate_ReferentialBridge(t *testing.T) {
	snap := buildSnapshot(&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0"})

	ok := mkOp(OpQosShapeCreate, qosShapeTarget(), &QosShapeCreateParams{Bridge: "vmbr0", RateMbit: 10})
	assertFindings(t, Validate([]Op{ok}, snap), nil)

	missing := mkOp(OpQosShapeCreate, qosShapeTarget(), &QosShapeCreateParams{Bridge: "vmbr9", RateMbit: 10})
	assertFindings(t, Validate([]Op{missing}, snap), []wantFinding{
		{SeverityError, codeQosBridgeNotFound, "qos-shape:pve1:shape1"},
	})
}
