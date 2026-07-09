package change

// IpamAllocCreateParams is op "ipam.alloc.create". Target is the parent
// SdnSubnet (Ref{Kind: KindSDNSubnet, ID: <cidr>}) the allocation is
// carved from; CIDR is the specific address being allocated (typically a
// /32 or /128 host route).
type IpamAllocCreateParams struct {
	CIDR     string `json:"cidr"`
	Hostname string `json:"hostname,omitempty"`
	MAC      string `json:"mac,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

func (IpamAllocCreateParams) isChangeParams() {}

// IpamAllocDeleteParams is op "ipam.alloc.delete": releases the allocation
// identified by CIDR within the target subnet.
type IpamAllocDeleteParams struct {
	CIDR string `json:"cidr"`
}

func (IpamAllocDeleteParams) isChangeParams() {}
