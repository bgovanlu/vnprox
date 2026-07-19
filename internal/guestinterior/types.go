package guestinterior

// Source names which read path produced a View (docs/api.md's Guest
// interior section).
type Source string

const (
	SourceQemuGA  Source = "qemu-ga"
	SourceLXCHost Source = "lxc-host"
)

// Interface is one network interface reported inside the guest.
type Interface struct {
	Name string `json:"name"`
	Mac  string `json:"mac,omitempty"`
	MTU  int    `json:"mtu,omitempty"`
	Up   bool   `json:"up"`
}

// Address is one address claimed on one of the guest's interfaces.
type Address struct {
	Interface string `json:"interface"`
	IP        string `json:"ip"`
	Family    string `json:"family"` // ipv4|ipv6
	Prefix    int    `json:"prefix,omitempty"`
}

// Route is one routing-table entry reported inside the guest.
type Route struct {
	Destination string `json:"destination"` // "default" or a CIDR
	Gateway     string `json:"gateway,omitempty"`
	Dev         string `json:"dev,omitempty"`
	Metric      int    `json:"metric,omitempty"`
}

// DNSConfig is the guest's resolver configuration.
type DNSConfig struct {
	Nameservers   []string `json:"nameservers,omitempty"`
	SearchDomains []string `json:"searchDomains,omitempty"`
}

// ListeningSocket is one listening TCP/UDP socket reported inside the
// guest.
type ListeningSocket struct {
	Proto     string `json:"proto"` // tcp|udp
	LocalAddr string `json:"localAddr"`
	LocalPort int    `json:"localPort"`
}

// View is one guest's interior read set (docs/api.md's Guest interior
// section: `{interfaces, addresses, routes, dns, listeningSockets,
// defaultGatewayReachable, source}`).
type View struct {
	Source                  Source            `json:"source"`
	DNS                     DNSConfig         `json:"dns"`
	Interfaces              []Interface       `json:"interfaces"`
	Addresses               []Address         `json:"addresses"`
	Routes                  []Route           `json:"routes"`
	ListeningSockets        []ListeningSocket `json:"listeningSockets"`
	DefaultGatewayReachable bool              `json:"defaultGatewayReachable"`
}

// defaultGateway returns the gateway address of routes' default route (the
// entry whose Destination is "default"), and whether one was found —
// shared by both the qemu and lxc fetch paths to decide what to probe for
// DefaultGatewayReachable.
func defaultGateway(routes []Route) (gw string, ok bool) {
	for _, r := range routes {
		if r.Destination == "default" && r.Gateway != "" {
			return r.Gateway, true
		}
	}
	return "", false
}
