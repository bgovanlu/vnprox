package guestinterior

import (
	"reflect"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pve"
)

func TestFromAgentInterfaces(t *testing.T) {
	ifaces := []pve.AgentIface{
		{Name: "lo", HardwareAddr: "00:00:00:00:00:00", IPAddresses: []pve.AgentIPAddress{{IPAddress: "127.0.0.1", IPAddressType: "ipv4", Prefix: 8}}},
		{Name: "eth0", HardwareAddr: "bc:24:11:aa:00:01", IPAddresses: []pve.AgentIPAddress{
			{IPAddress: "10.20.0.50", IPAddressType: "ipv4", Prefix: 24},
			{IPAddress: "fe80::1", IPAddressType: "ipv6", Prefix: 64},
		}},
	}
	interfaces, addresses := FromAgentInterfaces(ifaces)
	if len(interfaces) != 1 || interfaces[0].Name != "eth0" {
		t.Fatalf("interfaces = %+v, want exactly eth0 (loopback excluded)", interfaces)
	}
	if len(addresses) != 2 {
		t.Fatalf("addresses = %+v, want 2 (loopback excluded)", addresses)
	}
	if addresses[0].IP != "10.20.0.50" || addresses[0].Family != "ipv4" || addresses[0].Prefix != 24 {
		t.Errorf("addresses[0] = %+v, want 10.20.0.50/24 ipv4", addresses[0])
	}
}

func TestParseIPAddrJSON(t *testing.T) {
	tests := []struct {
		name          string
		raw           string
		wantFirstName string
		wantIfaces    int
		wantAddrs     int
		wantFirstUp   bool
	}{
		{
			name: "typical two-interface response",
			raw: `[
				{"ifname":"lo","flags":["LOOPBACK","UP"],"addr_info":[{"family":"inet","local":"127.0.0.1","prefixlen":8}]},
				{"ifname":"eth0","address":"bc:24:11:aa:00:02","mtu":1500,"flags":["BROADCAST","UP"],"addr_info":[{"family":"inet","local":"10.10.0.201","prefixlen":24},{"family":"inet6","local":"fe80::2","prefixlen":64}]}
			]`,
			wantIfaces: 1, wantAddrs: 2, wantFirstUp: true, wantFirstName: "eth0",
		},
		{
			name:       "interface down, no addresses",
			raw:        `[{"ifname":"eth1","address":"bc:24:11:aa:00:03","flags":["BROADCAST"],"addr_info":[]}]`,
			wantIfaces: 1, wantAddrs: 0, wantFirstUp: false, wantFirstName: "eth1",
		},
		{
			name: "empty array",
			raw:  `[]`,
		},
		{
			name: "empty payload",
			raw:  ``,
		},
		{
			name: "corrupt/truncated json",
			raw:  `[{"ifname":"eth0","addr_info":[{"family":"inet","local":`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			interfaces, addresses := ParseIPAddrJSON([]byte(tc.raw))
			if len(interfaces) != tc.wantIfaces {
				t.Errorf("interfaces = %+v, want len %d", interfaces, tc.wantIfaces)
			}
			if len(addresses) != tc.wantAddrs {
				t.Errorf("addresses = %+v, want len %d", addresses, tc.wantAddrs)
			}
			if tc.wantIfaces > 0 {
				if interfaces[0].Up != tc.wantFirstUp {
					t.Errorf("interfaces[0].Up = %v, want %v", interfaces[0].Up, tc.wantFirstUp)
				}
				if interfaces[0].Name != tc.wantFirstName {
					t.Errorf("interfaces[0].Name = %q, want %q", interfaces[0].Name, tc.wantFirstName)
				}
			}
		})
	}
}

func TestParseIPRouteJSON(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []Route
	}{
		{
			name: "default plus a scoped route",
			raw:  `[{"dst":"default","gateway":"10.10.0.1","dev":"eth0","metric":100},{"dst":"10.10.0.0/24","dev":"eth0"}]`,
			want: []Route{
				{Destination: "default", Gateway: "10.10.0.1", Dev: "eth0", Metric: 100},
				{Destination: "10.10.0.0/24", Dev: "eth0"},
			},
		},
		{
			name: "no default route",
			raw:  `[{"dst":"10.10.0.0/24","dev":"eth0"}]`,
			want: []Route{{Destination: "10.10.0.0/24", Dev: "eth0"}},
		},
		{name: "empty array", raw: `[]`, want: nil},
		{name: "empty payload", raw: ``, want: nil},
		{name: "corrupt json", raw: `[{"dst":`, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseIPRouteJSON([]byte(tc.raw))
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseIPRouteJSON() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDefaultGateway(t *testing.T) {
	routes := []Route{{Destination: "10.0.0.0/24", Dev: "eth0"}, {Destination: "default", Gateway: "10.0.0.1", Dev: "eth0"}}
	gw, ok := defaultGateway(routes)
	if !ok || gw != "10.0.0.1" {
		t.Fatalf("defaultGateway() = (%q, %v), want (10.0.0.1, true)", gw, ok)
	}
	if _, ok := defaultGateway(nil); ok {
		t.Fatalf("defaultGateway(nil) ok = true, want false")
	}
}

func TestParseResolvConf(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want DNSConfig
	}{
		{
			name: "typical resolv.conf",
			raw:  "# generated\nnameserver 1.1.1.1\nnameserver 8.8.8.8\nsearch example.com corp.internal\n",
			want: DNSConfig{Nameservers: []string{"1.1.1.1", "8.8.8.8"}, SearchDomains: []string{"example.com", "corp.internal"}},
		},
		{
			name: "empty file",
			raw:  "",
			want: DNSConfig{},
		},
		{
			name: "comments and blank lines only",
			raw:  "# nothing here\n; also a comment\n\n",
			want: DNSConfig{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseResolvConf(tc.raw)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseResolvConf() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseSS(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []ListeningSocket
	}{
		{
			name: "typical ss -H -tuln output",
			raw: "tcp   LISTEN 0      128          0.0.0.0:22         0.0.0.0:*\n" +
				"tcp   LISTEN 0      128             [::]:22            [::]:*\n" +
				"udp   UNCONN 0      0            0.0.0.0:68          0.0.0.0:*\n",
			want: []ListeningSocket{
				{Proto: "tcp", LocalAddr: "0.0.0.0", LocalPort: 22},
				{Proto: "tcp", LocalAddr: "::", LocalPort: 22},
				{Proto: "udp", LocalAddr: "0.0.0.0", LocalPort: 68},
			},
		},
		{
			name: "established tcp row is not listening",
			raw:  "tcp   ESTAB  0      0        10.0.0.5:22       10.0.0.9:5555\n",
			want: nil,
		},
		{name: "empty output", raw: "", want: nil},
		{name: "malformed line", raw: "not a valid ss line at all\n", want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseSS(tc.raw)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseSS() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
