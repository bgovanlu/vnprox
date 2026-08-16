package automation

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

// TargetPolicy is T-2905's webhook-destination guard. The daemon runs as
// root on a hypervisor with management-network reach and will POST
// attacker-shaped JSON to whatever URL a netWrite-capable user registers —
// a blind-SSRF primitive against loopback services, RFC1918 neighbors, and
// cloud metadata endpoints, unless the destination is constrained.
//
// The zero value is the production default: public https targets only.
// Each escape hatch is a config knob that warns at startup
// ([webhooks] allow_private_targets / allow_insecure_targets), never a
// request field — the same server-decides posture as [hub] trust_unsigned.
//
// Enforcement is two-layered on purpose: ValidateURL refuses at
// registration time (clear 400 for the operator), and GuardedClient
// re-checks the RESOLVED address at every dial (DNS can change between
// registration and delivery, and a hostname that resolves publicly today
// can resolve to 127.0.0.1 tomorrow — the classic rebinding move).
type TargetPolicy struct {
	AllowPrivate  bool
	AllowInsecure bool
}

// ValidateURL is the registration-time check: scheme policy, and — when
// the host is an IP literal — the address-class policy. Hostnames pass
// here (their addresses are only knowable at dial time) and are enforced
// by GuardedClient's dial hook instead.
func (p TargetPolicy) ValidateURL(raw string) error {
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("url must be an absolute http(s) URL")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !p.AllowInsecure {
			return fmt.Errorf("webhook targets must be https ([webhooks] allow_insecure_targets = true overrides, and warns at startup)")
		}
	default:
		return fmt.Errorf("url must be an absolute http(s) URL")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if err := p.checkIP(ip); err != nil {
			return err
		}
	}
	return nil
}

// checkIP refuses the address classes a webhook must not reach by default:
// loopback, RFC1918/ULA private, link-local (which covers 169.254.169.254,
// the cloud metadata address), and unspecified.
func (p TargetPolicy) checkIP(ip net.IP) error {
	if p.AllowPrivate {
		return nil
	}
	switch {
	case ip.IsLoopback(),
		ip.IsPrivate(),
		ip.IsLinkLocalUnicast(),
		ip.IsLinkLocalMulticast(),
		ip.IsUnspecified():
		return fmt.Errorf("webhook target %s is a non-public address ([webhooks] allow_private_targets = true overrides, and warns at startup)", ip)
	}
	return nil
}

// GuardedClient wraps base (nil = a default client) with a dialer whose
// Control hook re-applies checkIP to the address actually being connected
// to — the dispatch-time half of the two-layer guard. Control sees every
// connection attempt post-DNS, so a rebinding hostname is refused at the
// socket, not at the (already-passed) URL parse.
func (p TargetPolicy) GuardedClient(base *http.Client) *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("automation: parsing dial address %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("automation: dial address %q is not an IP", address)
			}
			return p.checkIP(ip)
		},
	}
	var out http.Client
	if base != nil {
		out = *base
	}
	var transport *http.Transport
	switch t := out.Transport.(type) {
	case *http.Transport:
		transport = t.Clone()
	default:
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, address)
	}
	out.Transport = transport
	return &out
}
