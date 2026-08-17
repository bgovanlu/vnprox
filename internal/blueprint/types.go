package blueprint

// CurrentBlueprintVersion is the only wire format version this package
// understands (docs/features/blueprints.md §1: "Format is versioned
// (blueprintVersion: 1)"). Instantiate/Validate reject any other value so
// a future format change fails loudly instead of silently misreading an
// older or newer file.
const CurrentBlueprintVersion = 1

// Kind names one of the six entity kinds a blueprint's EntityTemplate can
// describe. These are a v1 subset of inventory.Kind (docs/data-model.md
// §1) — the entity kinds the five documented starters need and the change
// engine can both validate and diff against inventory today. PhysNic/Guest/
// FwRuleset entities are not blueprint-manageable in v1 (no starter needs
// them; see the T-603 report for why "iface" was left out).
type Kind string

const (
	KindBridge    Kind = "bridge"
	KindBond      Kind = "bond"
	KindVlan      Kind = "vlan"
	KindSdnZone   Kind = "sdn-zone"
	KindSdnVnet   Kind = "sdn-vnet"
	KindSdnSubnet Kind = "sdn-subnet"
	// KindSdnController (T-3102) lets a blueprint create the SDN controller
	// a zone's own "controller" field references, closing the gap
	// starterEVPNDatacenter's own report used to document (T-603's "no
	// sdn.controller.create op; see the T-603 report").
	KindSdnController Kind = "sdn-controller"
)

var knownKinds = map[Kind]bool{
	KindBridge: true, KindBond: true, KindVlan: true,
	KindSdnZone: true, KindSdnVnet: true, KindSdnSubnet: true,
	KindSdnController: true,
}

// ParamType names the type validation/coercion applied to one blueprint
// parameter (docs/features/blueprints.md §1: "fill parameters (with
// validation ...)"; T-603 AC4: "bad CIDR/VID rejected"). AddressSuggest on
// a ParamDef of type ParamCIDR marks it eligible for the next-free-address
// suggestion (see suggest.go).
type ParamType string

const (
	ParamString   ParamType = "string" // free text (used for e.g. bridge/bond names)
	ParamInt      ParamType = "int"    // a bare integer
	ParamBool     ParamType = "bool"
	ParamCIDR     ParamType = "cidr"     // net.ParseCIDR-valid, e.g. "10.0.0.1/24" (an interface address)
	ParamIP       ParamType = "ip"       // net.ParseIP-valid, no prefix, e.g. "10.0.0.1" (a gateway)
	ParamVID      ParamType = "vid"      // single VLAN id, 1-4094
	ParamVIDList  ParamType = "vidList"  // array of VLAN ids, each 1-4094
	ParamIface    ParamType = "iface"    // a physical NIC name (non-empty string)
	ParamNodeList ParamType = "nodeList" // array of cluster node names
)

var knownParamTypes = map[ParamType]bool{
	ParamString: true, ParamInt: true, ParamBool: true, ParamCIDR: true, ParamIP: true,
	ParamVID: true, ParamVIDList: true, ParamIface: true, ParamNodeList: true,
}

// ParamDef is one parameter a blueprint's param form collects before
// instantiation.
type ParamDef struct {
	Default        any       `json:"default,omitempty"`
	Name           string    `json:"name"`
	Type           ParamType `json:"type"`
	Label          string    `json:"label,omitempty"`
	Description    string    `json:"description,omitempty"`
	Subnet         string    `json:"subnet,omitempty"`
	Required       bool      `json:"required,omitempty"`
	AddressSuggest bool      `json:"addressSuggest,omitempty"`
}

// NodeSelectorMode names how an EntityTemplate expands across cluster
// nodes (docs/features/blueprints.md §1: "per-node expansion selectors ...
// 'on every node', 'on nodes matching selector'").
type NodeSelectorMode string

const (
	// SelectAll expands the entity once per node in the instantiate
	// request's target node list ("on every node").
	SelectAll NodeSelectorMode = "all"
	// SelectSingle expands the entity exactly once, cluster-scoped (no
	// Node in its Ref) — used for the cluster-scoped SDN kinds.
	SelectSingle NodeSelectorMode = "single"
)

// NodeSelector is a blueprint- or entity-level node expansion rule.
type NodeSelector struct {
	Mode NodeSelectorMode `json:"mode"`
}

// EntityTemplate is one entity a blueprint creates: a Kind, an ID template
// (may contain {{param}} placeholders, substituted per docs/features/
// blueprints.md §1), an optional per-entity NodeSelector override, and a
// Fields map whose keys are exactly the corresponding change.*CreateParams
// struct's JSON field names (e.g. "vlanAware", "vids", "ports",
// "addresses" for KindBridge) — see adapters.go's field-name tables. Field
// values may be literals, "{{param}}" placeholder strings (whole-value
// substitution preserves the param's JSON type; partial matches like
// "{{zone}}/{{name}}" always produce a string), or the builtin
// "{{__nodes__}}" token (substituted with the instantiate request's target
// node list — used by the SDN zone starters' "nodes" field).
//
// Entities are expanded and diffed in list order, and blueprint authors
// are responsible for ordering dependencies correctly (a bond must appear
// before a bridge whose "ports" field references it) — the change engine's
// referential validator folds a changeset's ops forward in order
// (internal/change/validate_referential.go), the same convention every
// other programmatic op-drafting path in this codebase (e.g.
// web/src/changesets/dragDropOps.ts) already relies on.
type EntityTemplate struct {
	NodeSelector *NodeSelector  `json:"nodeSelector,omitempty"`
	Fields       map[string]any `json:"fields"`
	Kind         Kind           `json:"kind"`
	IDTemplate   string         `json:"idTemplate"`
}

// Blueprint is the versioned, parameterized topology template
// (docs/features/blueprints.md §1). ID is stable once saved (ULID for
// user-authored blueprints, a fixed "starter-*" slug for bundled ones).
// ReadOnly is true only for the five bundled starters — save/delete
// requests for those are always rejected (copy-to-edit is the documented
// workflow: the caller saves a new Blueprint derived from a starter's
// content instead).
type Blueprint struct {
	ID               string           `json:"id"`
	Name             string           `json:"name"`
	Description      string           `json:"description,omitempty"`
	NodeSelector     NodeSelector     `json:"nodeSelector"`
	CreatedBy        string           `json:"createdBy,omitempty"`
	Params           []ParamDef       `json:"params"`
	Entities         []EntityTemplate `json:"entities"`
	BlueprintVersion int              `json:"blueprintVersion"`
	CreatedAt        int64            `json:"createdAt,omitempty"`
	UpdatedAt        int64            `json:"updatedAt,omitempty"`
	ReadOnly         bool             `json:"readOnly,omitempty"`
}

// InstantiateRequest is POST /blueprints/{id}/instantiate's body
// (docs/api.md's Blueprints section: "{params} -> changeset draft"). Nodes
// is additive to the documented {params} shape (not in the original
// api.md contract; documented there in the same change per
// docs/development.md's definition-of-done #4, the established convention
// every other additive T-30x field in that doc already uses): the target
// cluster nodes for SelectAll expansion and for the SDN kinds' builtin
// "{{__nodes__}}" substitution. An empty/omitted Nodes defaults to every
// node currently in inventory.
type InstantiateRequest struct {
	Params map[string]any `json:"params"`
	Title  string         `json:"title,omitempty"`
	Nodes  []string       `json:"nodes,omitempty"`
}
