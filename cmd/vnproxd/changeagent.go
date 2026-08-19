package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/probe"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// Default node-file paths (overridable in tests). interfacesNewPath is the
// staged file ifupdown2 uses (docs/architecture.md §4).
const (
	interfacesFilePath = "/etc/network/interfaces"
	interfacesNewPath  = "/etc/network/interfaces.new"
)

// hostNodeAgent is the production change.NodeAgent: it stages and reloads the
// local node's /etc/network/interfaces as root (the "host writer" the change
// engine's per-node steps drive), and reads it back for snapshots and diffs.
//
// NEEDS HARDWARE VALIDATION: the reload path execs ifreload(8) and rewrites a
// real system file; it is exercised only by changeagent_test.go's temp-dir +
// fake-reload unit test, never against a live ifupdown2. Peer-node staging
// (a coordinating daemon writing another node's file via the peer API) is
// T-304's scope — this single-node-scoped agent operates on local paths for
// whatever node it's handed (correct for a single-node deployment; a cluster
// deployment must not route peer-node file steps here until T-304 lands).
type hostNodeAgent struct {
	reload         func(ctx context.Context) error
	log            *slog.Logger
	interfacesPath string
	pendingPath    string
	mu             sync.Mutex
}

// hostNodeAgent also satisfies peer.HostWriter (StageInterfaces/
// ReloadInterfaces already match; RestoreInterfaces below adds the third
// method), so the same production agent backs both the local change engine
// (change.NodeAgent) and this node's peer API host-write routes (T-301) —
// one implementation of "how vnproxd mutates this node's network files",
// not two.
var (
	_ change.NodeAgent = (*hostNodeAgent)(nil)
	_ peer.HostWriter  = (*hostNodeAgent)(nil)
)

func newHostNodeAgent(logger *slog.Logger) *hostNodeAgent {
	a := &hostNodeAgent{
		interfacesPath: interfacesFilePath,
		pendingPath:    interfacesNewPath,
		log:            logger,
	}
	a.reload = a.execIfreload
	return a
}

// devInterfacesSeed is the fixture newDevNodeAgent seeds an empty sandbox
// with, so ReadInterfaces/diff work out of the box on a fresh `make dev`.
const devInterfacesSeed = `# vnprox dev sandbox — this file stands in for /etc/network/interfaces.
# It is safe to edit or delete; it is re-seeded when missing.

auto lo
iface lo inet loopback

auto eno1
iface eno1 inet manual

auto vmbr0
iface vmbr0 inet static
	address 192.0.2.10/24
	gateway 192.0.2.1
	bridge-ports eno1
	bridge-stp off
	bridge-fd 0
`

// newDevNodeAgent is the sandboxed dev variant of newHostNodeAgent
// ([safety] dev_interfaces_dir; audit-phase-2 F-22): the same staging/
// commit/backup logic, but operating on <dir>/interfaces(.new) instead of
// the real /etc/network files, with the ifreload step replaced by a logged
// no-op — so a `make dev` daemon can exercise the full diff/apply/rollback
// flow without ever being able to reconfigure the developer's machine.
func newDevNodeAgent(dir string, logger *slog.Logger) (*hostNodeAgent, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating dev interfaces sandbox %s: %w", dir, err)
	}
	path := filepath.Join(dir, "interfaces")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if writeErr := os.WriteFile(path, []byte(devInterfacesSeed), 0o644); writeErr != nil {
			return nil, fmt.Errorf("seeding dev interfaces sandbox %s: %w", path, writeErr)
		}
	} else if err != nil {
		return nil, fmt.Errorf("checking dev interfaces sandbox %s: %w", path, err)
	}
	a := &hostNodeAgent{
		interfacesPath: path,
		pendingPath:    filepath.Join(dir, "interfaces.new"),
		log:            logger,
	}
	a.reload = func(context.Context) error {
		logger.Info("change: dev sandbox agent — skipping real ifreload", "interfaces", path)
		return nil
	}
	return a, nil
}

func (a *hostNodeAgent) execIfreload(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "ifreload", "-a")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ifreload -a: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (a *hostNodeAgent) ReadInterfaces(_ context.Context, _ string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, err := os.ReadFile(a.interfacesPath)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", a.interfacesPath, err)
	}
	return string(b), nil
}

func (a *hostNodeAgent) StageInterfaces(_ context.Context, _, content string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.WriteFile(a.pendingPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("staging %s: %w", a.pendingPath, err)
	}
	return nil
}

// ReloadInterfaces applies the staged file and reloads the network, with a
// backup-and-restore so a failed reload leaves the committed file exactly as
// it was (the NodeAgent contract): read the staged file, back up the current
// committed file, move the staged file into place, ifreload; on failure,
// restore the backup and re-reload before returning the error.
func (a *hostNodeAgent) ReloadInterfaces(ctx context.Context, node string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	staged, err := os.ReadFile(a.pendingPath)
	if err != nil {
		return fmt.Errorf("reading staged %s: %w", a.pendingPath, err)
	}
	backup, err := os.ReadFile(a.interfacesPath)
	if err != nil {
		return fmt.Errorf("reading %s for backup: %w", a.interfacesPath, err)
	}
	if err := os.WriteFile(a.interfacesPath, staged, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", a.interfacesPath, err)
	}
	if err := a.reload(ctx); err != nil {
		if restoreErr := os.WriteFile(a.interfacesPath, backup, 0o644); restoreErr != nil {
			a.log.Error("change: failed to restore interfaces after failed reload", "node", node, "error", restoreErr)
		} else if reErr := a.reload(ctx); reErr != nil {
			a.log.Error("change: failed to re-reload after restoring interfaces", "node", node, "error", reErr)
		}
		return fmt.Errorf("reloading network on %s: %w", node, err)
	}
	_ = os.Remove(a.pendingPath)
	return nil
}

func (a *hostNodeAgent) DiscardStaged(_ context.Context, _ string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.Remove(a.pendingPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("discarding staged %s: %w", a.pendingPath, err)
	}
	return nil
}

// RestoreInterfaces implements peer.HostWriter for T-301's
// POST /api/peer/host/restore: write content directly as the committed
// interfaces file and reload, bypassing the normal stage/review flow. This
// is the rollback path (T-304's distributed commit-confirm timers restore
// a known-good pre-apply snapshot under time pressure, not a
// user-reviewed draft), so it reuses StageInterfaces/ReloadInterfaces'
// existing backup-and-restore-on-failure guarantee rather than duplicating
// it.
func (a *hostNodeAgent) RestoreInterfaces(ctx context.Context, node, content string) error {
	if err := a.StageInterfaces(ctx, node, content); err != nil {
		return fmt.Errorf("staging restore content for %s: %w", node, err)
	}
	if err := a.ReloadInterfaces(ctx, node); err != nil {
		return fmt.Errorf("reloading restored interfaces for %s: %w", node, err)
	}
	return nil
}

// pveGatewayProvider builds a change.PVEGateway from the requesting session's
// own PVE client (docs/architecture.md §6: cluster-scope writes use the
// user's ticket). It satisfies api.PVEGatewayProvider.
type pveGatewayProvider struct {
	auth *auth.Service
}

func (p pveGatewayProvider) GatewayFor(ctx context.Context) (change.PVEGateway, bool) {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return nil, false
	}
	client, ok := p.auth.PVEClientFor(id.SessionID)
	if !ok {
		return nil, false
	}
	return &pveGateway{client: client}, true
}

// RevertTicket implements change.RevertTicketSource (T-1805 / roadmap-proven
// D1): it hands the change engine the applying user's own PVE ticket so the
// engine can seal it for the duration of the commit-confirm window and revert
// this changeset's `fw.*`/`sdn.*` ops with it if the window closes
// unconfirmed.
//
// ok is false for any client with no user ticket to give — notably the
// daemon's read-only `vnprox@pve!daemon` API-token identity, which must never
// be substituted for one: a revert performed as vnprox rather than as the user
// is the standing-privileged-credential design D1 explicitly rejected. The
// change engine's only response to false is to report unattended revert as
// unavailable, which is the pre-T-1805 behaviour stated out loud.
func (g *pveGateway) RevertTicket(ctx context.Context) (change.RevertTicket, bool) {
	ticket, csrf, issuedAt, ok := g.client.RevertCredentials(ctx)
	if !ok || ticket == "" || csrf == "" {
		return change.RevertTicket{}, false
	}
	return change.RevertTicket{
		Ticket:    ticket,
		CSRF:      csrf,
		ExpiresAt: change.TicketExpiryFrom(issuedAt, pve.TicketLifetime),
	}, true
}

// revertGatewayFactory rebuilds a change.PVEGateway from a sealed revert
// ticket, for the unattended revert paths (commit-confirm timeout, crash
// recovery) which by construction hold no live session. It satisfies
// change.RevertGatewayFactory.
//
// The client it builds is a **sealed-ticket** client (pve.Config.Ticket/
// CSRFToken): it authenticates with exactly the unsealed ticket and never
// renews and never logs in, so the daemon cannot use a sealed credential to
// mint itself a fresh, longer-lived one. When the ticket has expired PVE
// answers 401 and the revert reports the firewall/SDN scopes it could not
// restore — the changeset then lands in the distinguishable "rollback
// incomplete" state rather than a silent `rolled_back`.
//
// tls mirrors the login client's own trust decision (cmd/vnproxd/auth.go):
// pinned to the node's pveproxy certificate in a real deployment, unpinned
// against a plain-HTTP pvemock in dev/test.
type revertGatewayFactory struct {
	// httpClient (T-2801) is the demo daemon's in-process transport, or nil
	// for a normal daemon. A demo never reaches this path — no changeset can
	// be applied, so no revert ticket can exist — but leaving a real dialer
	// wired into a demo daemon's revert factory would be one refactor away
	// from an unattended revert against something real.
	httpClient *http.Client
	apiURL     string
	tls        pve.TLSConfig
}

func (f revertGatewayFactory) GatewayForRevertTicket(_ context.Context, t change.RevertTicket) (change.PVEGateway, error) {
	client, err := pve.New(pve.Config{
		APIURL:     f.apiURL,
		Auth:       pve.AuthTicket,
		Ticket:     t.Ticket,
		CSRFToken:  t.CSRF,
		TLS:        f.tls,
		HTTPClient: f.httpClient,
	})
	if err != nil {
		// Deliberately no ticket material in the error: this string reaches
		// the daemon log.
		return nil, fmt.Errorf("building sealed-ticket PVE client for unattended revert: %w", err)
	}
	return &pveGateway{client: client}, nil
}

// probeClientProvider builds a probe.PVEExecer from the requesting
// session's own PVE client (T-802: guest-agent live path probes reach into
// a guest under the user's own ticket, the same docs/architecture.md §6
// convention pveGatewayProvider above follows for changeset apply/rollback).
// It satisfies api.ProbeClientProvider.
type probeClientProvider struct {
	auth *auth.Service
}

func (p probeClientProvider) ProbeClientFor(ctx context.Context) (probe.PVEExecer, bool) {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return nil, false
	}
	client, ok := p.auth.PVEClientFor(id.SessionID)
	if !ok {
		return nil, false
	}
	return client, true
}

// pveGateway realizes every change.PVEGateway method — the sdn.zone/vnet/
// subnet.* stage ops and sdn.apply step (T-402), the full fw.* op family
// (T-502), and the ipam.alloc.* op family (T-405) — through the user's own
// client (docs/architecture.md §6).
type pveGateway struct {
	client *pve.Client
}

// SDNStageOp implements change.PVEGateway: dispatches one sdn.zone/vnet/
// subnet create/update/delete op to the matching pve.Client write call.
// Update ops only set the fields their params carry (nil pointer fields
// leave the corresponding wire field zero-valued, matching a partial PUT —
// real PVE/pvemock's update handlers replace the whole object server-side
// per this codebase's existing SDNZoneSpec/SDNVnetSpec/SDNSubnetSpec update
// handlers, so a caller wanting a true partial merge must read-modify-write;
// change.Service's update ops are themselves already the merged intent by
// the time they reach here — see params_sdn.go's *UpdateParams doc
// comments — so this is not a functional gap for the ops this package's
// own validators/projection produce).
func (g *pveGateway) SDNStageOp(ctx context.Context, op change.Op, subnetVnet string) error {
	switch p := op.Params.(type) {
	case *change.SdnZoneCreateParams:
		return g.client.CreateSDNZone(ctx, pve.SDNZone{
			ID: op.Target.ID, Type: p.Type, Bridge: p.Bridge, Controller: p.Controller, IPAM: p.IPAM,
			Nodes: p.Nodes, ExitNodes: p.ExitNodes, Peers: p.Peers, VrfVxlan: p.VrfVxlan, MTU: p.MTU,
		})
	case *change.SdnZoneUpdateParams:
		z := pve.SDNZone{ID: op.Target.ID}
		if p.Bridge != nil {
			z.Bridge = *p.Bridge
		}
		if p.Controller != nil {
			z.Controller = *p.Controller
		}
		if p.IPAM != nil {
			z.IPAM = *p.IPAM
		}
		if p.Nodes != nil {
			z.Nodes = *p.Nodes
		}
		if p.ExitNodes != nil {
			z.ExitNodes = *p.ExitNodes
		}
		if p.Peers != nil {
			z.Peers = *p.Peers
		}
		if p.VrfVxlan != nil {
			z.VrfVxlan = *p.VrfVxlan
		}
		if p.MTU != nil {
			z.MTU = *p.MTU
		}
		return g.client.UpdateSDNZone(ctx, op.Target.ID, z)
	case *change.SdnZoneDeleteParams:
		return g.client.DeleteSDNZone(ctx, op.Target.ID)

	case *change.SdnVnetCreateParams:
		return g.client.CreateSDNVnet(ctx, pve.SDNVnet{
			ID: pve.SDNVnetID(op.Target.ID), Zone: p.Zone, Alias: p.Alias, Tag: p.Tag, VlanAware: p.VlanAware,
		})
	case *change.SdnVnetUpdateParams:
		id := pve.SDNVnetID(op.Target.ID)
		v := pve.SDNVnet{ID: id}
		if p.Alias != nil {
			v.Alias = *p.Alias
		}
		if p.Tag != nil {
			v.Tag = *p.Tag
		}
		if p.VlanAware != nil {
			v.VlanAware = *p.VlanAware
		}
		return g.client.UpdateSDNVnet(ctx, id, v)
	case *change.SdnVnetDeleteParams:
		return g.client.DeleteSDNVnet(ctx, pve.SDNVnetID(op.Target.ID))

	case *change.SdnSubnetCreateParams:
		start, end := firstDHCPRange(p.DHCPRanges)
		return g.client.CreateSDNSubnet(ctx, pve.SDNVnetID(p.Vnet), pve.SDNSubnet{
			ID: pve.SDNSubnetID(op.Target.ID), Vnet: pve.SDNVnetID(p.Vnet), CIDR: p.CIDR, Gateway: p.Gateway,
			DHCPRangeStart: start, DHCPRangeEnd: end, SNAT: p.SNAT,
		})
	case *change.SdnSubnetUpdateParams:
		vnet := pve.SDNVnetID(subnetVnet)
		s := pve.SDNSubnet{ID: pve.SDNSubnetID(op.Target.ID), Vnet: vnet, CIDR: op.Target.ID}
		if p.Gateway != nil {
			s.Gateway = *p.Gateway
		}
		if p.DHCPRanges != nil {
			s.DHCPRangeStart, s.DHCPRangeEnd = firstDHCPRange(*p.DHCPRanges)
		}
		if p.SNAT != nil {
			s.SNAT = *p.SNAT
		}
		return g.client.UpdateSDNSubnet(ctx, vnet, pve.SDNSubnetID(op.Target.ID), s)
	case *change.SdnSubnetDeleteParams:
		return g.client.DeleteSDNSubnet(ctx, pve.SDNVnetID(subnetVnet), pve.SDNSubnetID(op.Target.ID))

	// T-1204 SDN DNS ops. Zone configs stage into /etc/pve/sdn/dns.cfg (like
	// zones/vnets/subnets); records are written straight into the backing
	// PowerDNS server per-record. The record target's Ref.ID is the
	// "<zone>/<name>/<type>" composite — the params carry those fields
	// explicitly for create; update/delete recover them from the target id.
	case *change.SdnDnsZoneCreateParams:
		return g.client.CreateSDNDnsZone(ctx, pve.SDNDnsZone{ID: op.Target.ID, DNS: p.DNS, TTL: p.TTL})
	case *change.SdnDnsZoneUpdateParams:
		z := pve.SDNDnsZone{ID: op.Target.ID}
		if p.DNS != nil {
			z.DNS = *p.DNS
		}
		if p.TTL != nil {
			z.TTL = *p.TTL
		}
		return g.client.UpdateSDNDnsZone(ctx, op.Target.ID, z)
	case *change.SdnDnsZoneDeleteParams:
		return g.client.DeleteSDNDnsZone(ctx, op.Target.ID)

	case *change.SdnDnsRecordCreateParams:
		return g.client.CreateSDNDnsRecord(ctx, p.Zone, pve.SDNDnsRecord{Name: p.Name, Type: p.Type, Value: p.Value, TTL: p.TTL})
	case *change.SdnDnsRecordUpdateParams:
		zone, name, typ := splitDNSRecordID(op.Target.ID)
		rec := pve.SDNDnsRecord{Name: name, Type: typ}
		if p.Value != nil {
			rec.Value = *p.Value
		}
		if p.TTL != nil {
			rec.TTL = *p.TTL
		}
		return g.client.UpdateSDNDnsRecord(ctx, zone, name, typ, rec)
	case *change.SdnDnsRecordDeleteParams:
		zone, name, typ := splitDNSRecordID(op.Target.ID)
		return g.client.DeleteSDNDnsRecord(ctx, zone, name, typ)

	// T-3101 SDN Fabric ops. Like DNS above, these stage into the same
	// PUT /cluster/sdn commit — no bespoke apply path (op.go's
	// OpSdnFabricCreate doc comment).
	case *change.SdnFabricCreateParams:
		return g.client.CreateSDNFabric(ctx, pve.SDNFabric{
			ID: op.Target.ID, Protocol: p.Protocol, IPPrefix: p.IPPrefix, IP6Prefix: p.IP6Prefix,
			CSNPInterval: p.CSNPInterval, HelloInterval: p.HelloInterval, RouteFilter: p.RouteFilter,
			Area: p.Area, Redistribute: p.Redistribute, PersistentKeepalive: p.PersistentKeepalive,
		})
	case *change.SdnFabricUpdateParams:
		f := pve.SDNFabric{ID: op.Target.ID}
		if p.IPPrefix != nil {
			f.IPPrefix = *p.IPPrefix
		}
		if p.IP6Prefix != nil {
			f.IP6Prefix = *p.IP6Prefix
		}
		if p.CSNPInterval != nil {
			f.CSNPInterval = *p.CSNPInterval
		}
		if p.HelloInterval != nil {
			f.HelloInterval = *p.HelloInterval
		}
		if p.RouteFilter != nil {
			f.RouteFilter = *p.RouteFilter
		}
		if p.Area != nil {
			f.Area = *p.Area
		}
		if p.Redistribute != nil {
			f.Redistribute = *p.Redistribute
		}
		if p.PersistentKeepalive != nil {
			f.PersistentKeepalive = *p.PersistentKeepalive
		}
		return g.client.UpdateSDNFabric(ctx, op.Target.ID, f)
	case *change.SdnFabricDeleteParams:
		return g.client.DeleteSDNFabric(ctx, op.Target.ID)

	// T-3102 SDN Controller ops. Like fabrics above, these stage into the
	// same PUT /cluster/sdn commit — no bespoke apply path (op.go's
	// OpSdnControllerCreate doc comment).
	case *change.SdnControllerCreateParams:
		return g.client.CreateSDNController(ctx, pve.SDNController{
			ID: op.Target.ID, Type: p.Type, BgpMode: p.BgpMode, Fabric: p.Fabric,
			IsisDomain: p.IsisDomain, IsisNet: p.IsisNet, Loopback: p.Loopback, Node: p.Node,
			PeerGroupName: p.PeerGroupName, RouteMapIn: p.RouteMapIn, RouteMapOut: p.RouteMapOut,
			Nodes: p.Nodes, Peers: p.Peers, IsisIfaces: p.IsisIfaces,
			ASN: p.ASN, EbgpMultihop: p.EbgpMultihop, Ebgp: p.Ebgp, BgpMultipathAsPathRelax: p.BgpMultipathAsPathRelax,
		})
	case *change.SdnControllerUpdateParams:
		ctl := pve.SDNController{ID: op.Target.ID}
		if p.BgpMode != nil {
			ctl.BgpMode = *p.BgpMode
		}
		if p.Fabric != nil {
			ctl.Fabric = *p.Fabric
		}
		if p.IsisDomain != nil {
			ctl.IsisDomain = *p.IsisDomain
		}
		if p.IsisNet != nil {
			ctl.IsisNet = *p.IsisNet
		}
		if p.Loopback != nil {
			ctl.Loopback = *p.Loopback
		}
		if p.Node != nil {
			ctl.Node = *p.Node
		}
		if p.PeerGroupName != nil {
			ctl.PeerGroupName = *p.PeerGroupName
		}
		if p.RouteMapIn != nil {
			ctl.RouteMapIn = *p.RouteMapIn
		}
		if p.RouteMapOut != nil {
			ctl.RouteMapOut = *p.RouteMapOut
		}
		if p.Nodes != nil {
			ctl.Nodes = *p.Nodes
		}
		if p.Peers != nil {
			ctl.Peers = *p.Peers
		}
		if p.IsisIfaces != nil {
			ctl.IsisIfaces = *p.IsisIfaces
		}
		if p.ASN != nil {
			ctl.ASN = *p.ASN
		}
		if p.EbgpMultihop != nil {
			ctl.EbgpMultihop = *p.EbgpMultihop
		}
		if p.Ebgp != nil {
			ctl.Ebgp = *p.Ebgp
		}
		if p.BgpMultipathAsPathRelax != nil {
			ctl.BgpMultipathAsPathRelax = *p.BgpMultipathAsPathRelax
		}
		return g.client.UpdateSDNController(ctx, op.Target.ID, ctl)
	case *change.SdnControllerDeleteParams:
		return g.client.DeleteSDNController(ctx, op.Target.ID)

	// T-3104 SDN IPAM plugin-instance ops. Like fabrics/controllers above,
	// these stage into the same PUT /cluster/sdn commit — no bespoke apply
	// path (op.go's OpSdnIpamCreate doc comment).
	case *change.SdnIpamCreateParams:
		return g.client.CreateIPAM(ctx, pve.IPAM{
			ID: op.Target.ID, Type: p.Type, URL: p.URL, Token: p.Token,
			Fingerprint: p.Fingerprint, Section: p.Section,
		})
	case *change.SdnIpamUpdateParams:
		ip := pve.IPAM{ID: op.Target.ID}
		if p.URL != nil {
			ip.URL = *p.URL
		}
		if p.Token != nil {
			ip.Token = *p.Token
		}
		if p.Fingerprint != nil {
			ip.Fingerprint = *p.Fingerprint
		}
		if p.Section != nil {
			ip.Section = *p.Section
		}
		return g.client.UpdateIPAM(ctx, op.Target.ID, ip)
	case *change.SdnIpamDeleteParams:
		return g.client.DeleteIPAM(ctx, op.Target.ID)

	default:
		return fmt.Errorf("changeagent: SDNStageOp: unsupported op type %q", op.Type)
	}
}

// splitDNSRecordID splits a sdn-dns-record Ref.ID's "<zone>/<name>/<type>"
// composite (T-1204) into its three parts. A record's name may itself
// contain no "/" (a hostname label or FQDN), so the split is on the first and
// last "/": everything before the first is the zone, everything after the
// last is the type, the middle is the name.
func splitDNSRecordID(id string) (zone, name, typ string) {
	first := strings.IndexByte(id, '/')
	last := strings.LastIndexByte(id, '/')
	if first < 0 || last <= first {
		return id, "", ""
	}
	return id[:first], id[first+1 : last], id[last+1:]
}

// firstDHCPRange splits ranges[0] ("start-end", change.validDHCPRange's
// shape) into its two endpoints for pve.SDNSubnet's single-range wire
// field. Real PVE (and this codebase's pve.SDNSubnet/pvemock SDNSubnetSpec,
// unmodified by this task) models only one DHCP range per subnet and has no
// DNSZonePrefix wire field at all — change.SdnSubnetCreateParams'
// DNSZonePrefix and any DHCPRanges beyond the first have no PVE-side
// representation to stage yet (a documented, pre-existing gap — see the
// T-402 report).
func firstDHCPRange(ranges []string) (start, end string) {
	if len(ranges) == 0 {
		return "", ""
	}
	parts := strings.SplitN(ranges[0], "-", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// ApplySDN implements change.PVEGateway: PUT /cluster/sdn, wait for the
// task, then read back affectedZones' per-node status for T-402's post-apply
// health verification (docs/features/sdn.md §4). The result (UPID/Node) is
// populated before the wait even starts, so a caller gets a task-log deep
// link regardless of whether the task itself or the health check is what
// ultimately fails.
func (g *pveGateway) ApplySDN(ctx context.Context, affectedZones []string) (change.SDNApplyResult, error) {
	upid, err := g.client.ApplySDN(ctx)
	if err != nil {
		return change.SDNApplyResult{}, err
	}
	node := upidNode(upid)
	result := change.SDNApplyResult{UPID: upid, Node: node}

	if _, err := g.client.WaitTask(ctx, node, upid, pve.WaitOptions{Timeout: 5 * time.Minute}); err != nil {
		return result, err
	}

	for _, zoneID := range affectedZones {
		statuses, err := g.client.GetSDNZoneStatus(ctx, zoneID)
		if err != nil {
			return result, fmt.Errorf("changeagent: reading post-apply status for sdn zone %s: %w", zoneID, err)
		}
		zh := change.SDNZoneHealth{Zone: zoneID}
		for _, st := range statuses {
			zh.Nodes = append(zh.Nodes, change.SDNNodeHealth{Node: st.Node, Status: st.Status, Detail: st.Detail})
		}
		result.Zones = append(result.Zones, zh)
	}
	return result, nil
}

// SDNConfig implements change.PVEGateway: reads the full staged (pending-
// merged) zone/vnet/subnet tree, T-402's pre-apply/rollback snapshot source
// (see that interface method's doc comment for why "staged" and not
// "?running=1").
func (g *pveGateway) SDNConfig(ctx context.Context) (change.SDNConfig, error) {
	zones, err := g.client.ListSDNZones(ctx)
	if err != nil {
		return change.SDNConfig{}, fmt.Errorf("changeagent: listing sdn zones: %w", err)
	}
	vnets, err := g.client.ListSDNVnets(ctx)
	if err != nil {
		return change.SDNConfig{}, fmt.Errorf("changeagent: listing sdn vnets: %w", err)
	}

	var cfg change.SDNConfig
	for _, z := range zones {
		cfg.Zones = append(cfg.Zones, change.SDNZoneConfig{
			ID: z.ID, Type: z.Type, Bridge: z.Bridge, Controller: z.Controller, IPAM: z.IPAM,
			Nodes: z.Nodes, ExitNodes: z.ExitNodes, Peers: z.Peers, VrfVxlan: z.VrfVxlan, MTU: z.MTU,
		})
	}
	for _, v := range vnets {
		// SDNVnetConfig.ID is internal/change's "<zone>/<vnet>" Ref.ID
		// convention (see pve.SDNVnetID's doc comment), reconstructed here
		// from PVE's own bare vnet id (v.ID) + zone (v.Zone) — not v.ID
		// alone.
		refID := v.Zone + "/" + v.ID
		cfg.Vnets = append(cfg.Vnets, change.SDNVnetConfig{
			ID: refID, Zone: v.Zone, Alias: v.Alias, Tag: v.Tag, VlanAware: v.VlanAware,
		})
		subnets, subErr := g.client.ListSDNSubnets(ctx, v.ID)
		if subErr != nil {
			return change.SDNConfig{}, fmt.Errorf("changeagent: listing sdn subnets for vnet %s: %w", v.ID, subErr)
		}
		for _, s := range subnets {
			var ranges []string
			if s.DHCPRangeStart != "" && s.DHCPRangeEnd != "" {
				ranges = []string{s.DHCPRangeStart + "-" + s.DHCPRangeEnd}
			}
			// SDNSubnetConfig.ID is the CIDR (internal/change's convention
			// throughout — see pve.SDNSubnetID's doc comment), not PVE's
			// own dash-form wire id (s.ID) used only for the URL path
			// segment; Vnet is likewise the "<zone>/<vnet>" Ref.ID form,
			// not PVE's bare s.Vnet.
			cfg.Subnets = append(cfg.Subnets, change.SDNSubnetConfig{
				ID: s.CIDR, Vnet: refID, Gateway: s.Gateway, DHCPRanges: ranges, SNAT: s.SNAT,
			})
		}
	}

	// T-1204: DNS zones + their records, for the same pre-apply/rollback
	// snapshot. A cluster with no DNS plugin configured contributes nothing.
	dnsZones, err := g.client.ListSDNDnsZones(ctx)
	if err != nil {
		return change.SDNConfig{}, fmt.Errorf("changeagent: listing sdn dns zones: %w", err)
	}
	for _, z := range dnsZones {
		cfg.DnsZones = append(cfg.DnsZones, change.SDNDnsZoneConfig{ID: z.ID, DNS: z.DNS, TTL: z.TTL})
		recs, recErr := g.client.ListSDNDnsRecords(ctx, z.ID)
		if recErr != nil {
			return change.SDNConfig{}, fmt.Errorf("changeagent: listing sdn dns records for zone %s: %w", z.ID, recErr)
		}
		for _, r := range recs {
			cfg.DnsRecords = append(cfg.DnsRecords, change.SDNDnsRecordConfig{
				ID: z.ID + "/" + r.Name + "/" + r.Type, Zone: z.ID, Name: r.Name, Type: r.Type, Value: r.Value, TTL: r.TTL,
			})
		}
	}

	// T-3101: fabrics, for the same pre-apply/rollback snapshot.
	fabrics, err := g.client.ListSDNFabrics(ctx)
	if err != nil {
		return change.SDNConfig{}, fmt.Errorf("changeagent: listing sdn fabrics: %w", err)
	}
	for _, f := range fabrics {
		cfg.Fabrics = append(cfg.Fabrics, change.SDNFabricConfig{
			ID: f.ID, Protocol: f.Protocol, IPPrefix: f.IPPrefix, IP6Prefix: f.IP6Prefix,
			CSNPInterval: f.CSNPInterval, HelloInterval: f.HelloInterval, RouteFilter: f.RouteFilter,
			Area: f.Area, Redistribute: f.Redistribute, PersistentKeepalive: f.PersistentKeepalive,
		})
	}

	// T-3102: controllers, for the same pre-apply/rollback snapshot.
	controllers, err := g.client.ListSDNControllers(ctx)
	if err != nil {
		return change.SDNConfig{}, fmt.Errorf("changeagent: listing sdn controllers: %w", err)
	}
	for _, c := range controllers {
		cfg.Controllers = append(cfg.Controllers, change.SDNControllerConfig{
			ID: c.ID, Type: c.Type, BgpMode: c.BgpMode, Fabric: c.Fabric,
			IsisDomain: c.IsisDomain, IsisNet: c.IsisNet, Loopback: c.Loopback, Node: c.Node,
			PeerGroupName: c.PeerGroupName, RouteMapIn: c.RouteMapIn, RouteMapOut: c.RouteMapOut,
			Nodes: c.Nodes, Peers: c.Peers, IsisIfaces: c.IsisIfaces,
			ASN: c.ASN, EbgpMultihop: c.EbgpMultihop, Ebgp: c.Ebgp, BgpMultipathAsPathRelax: c.BgpMultipathAsPathRelax,
		})
	}

	// T-3104: ipam plugin instances, for the same pre-apply/rollback
	// snapshot. Token is never populated (ListIPAMs never returns it — see
	// internal/pve/sdn_ipam.go's package doc comment), so SDNIpamConfig
	// simply carries none.
	ipams, err := g.client.ListIPAMs(ctx)
	if err != nil {
		return change.SDNConfig{}, fmt.Errorf("changeagent: listing sdn ipams: %w", err)
	}
	for _, i := range ipams {
		cfg.Ipams = append(cfg.Ipams, change.SDNIpamConfig{
			ID: i.ID, Type: i.Type, URL: i.URL, Fingerprint: i.Fingerprint, Section: i.Section,
		})
	}

	return cfg, nil
}

// SDNPendingForeign implements change.PVEGateway: reads real PVE's own
// "?pending=1" view (pve.ListSDNZonesPending/ListSDNVnetsPending/
// ListSDNSubnetsPending, added for T-3101-followup-01 — see
// planning/reports/evidence/pve-9.2.4-sdn-pending-state.txt) and reports
// every zone/vnet/subnet currently staged-but-not-yet-applied, regardless
// of whether this daemon's own change engine staged it.
//
// Scoped to zones/vnets/subnets only. Controllers and fabrics also support
// "?pending=1" against real PVE (confirmed in the evidence file) but are
// deliberately left for a follow-up — see this task's completion report —
// and ipam plugin instances have no pending view in real PVE's API at all
// (Ipams.pm carries no "pending" param; confirmed by grep against
// pvecube's own perl source), so they can never appear here regardless.
//
// Subnets are nested under a vnet in PVE's URL space, so this enumerates
// every currently-known vnet (the plain, non-pending list — mirroring
// SDNConfig's own subnet loop above, including a vnet that is itself only
// staged-pending "new": it still resolves, since the URL path segment
// names a config-file entry that already exists in the staged file) and
// reads each one's pending subnets in turn.
func (g *pveGateway) SDNPendingForeign(ctx context.Context) ([]change.SDNPendingEntry, error) {
	var out []change.SDNPendingEntry

	zones, err := g.client.ListSDNZonesPending(ctx)
	if err != nil {
		return nil, fmt.Errorf("changeagent: listing pending sdn zones: %w", err)
	}
	for _, z := range zones {
		if z.State == "" {
			continue
		}
		out = append(out, change.SDNPendingEntry{Kind: z.Kind, ID: z.ID, State: string(z.State), Fields: z.Fields})
	}

	vnets, err := g.client.ListSDNVnetsPending(ctx)
	if err != nil {
		return nil, fmt.Errorf("changeagent: listing pending sdn vnets: %w", err)
	}
	for _, v := range vnets {
		if v.State == "" {
			continue
		}
		out = append(out, change.SDNPendingEntry{Kind: v.Kind, ID: v.ID, State: string(v.State), Fields: v.Fields})
	}

	allVnets, err := g.client.ListSDNVnets(ctx)
	if err != nil {
		return nil, fmt.Errorf("changeagent: listing sdn vnets: %w", err)
	}
	for _, v := range allVnets {
		subs, subErr := g.client.ListSDNSubnetsPending(ctx, v.ID)
		if subErr != nil {
			return nil, fmt.Errorf("changeagent: listing pending sdn subnets for vnet %s: %w", v.ID, subErr)
		}
		for _, sub := range subs {
			if sub.State == "" {
				continue
			}
			out = append(out, change.SDNPendingEntry{Kind: sub.Kind, ID: sub.ID, State: string(sub.State), Fields: sub.Fields})
		}
	}

	return out, nil
}

// AllocateIPAMAddress realizes T-405's ipam.alloc.create op: PVE's IPAM
// write is a synchronous API call (no task), unlike ApplySDN above.
// subnetCIDR is recorded on the created entry so internal/ipam's
// per-subnet bucketing (which keys off exactly this field) finds it.
func (g *pveGateway) AllocateIPAMAddress(ctx context.Context, vnet, subnetCIDR string, alloc change.IpamAllocCreateParams) error {
	return g.client.CreateIPAMAllocation(ctx, vnet, pve.IPAMAllocation{
		IP:       allocHostAddr(alloc.CIDR),
		MAC:      alloc.MAC,
		Hostname: alloc.Hostname,
		Subnet:   subnetCIDR,
	})
}

// ReleaseIPAMAddress realizes T-405's ipam.alloc.delete op.
func (g *pveGateway) ReleaseIPAMAddress(ctx context.Context, vnet, subnetCIDR, cidr string) error {
	return g.client.DeleteIPAMAllocation(ctx, vnet, allocHostAddr(cidr), subnetCIDR)
}

// allocHostAddr reduces an ipam.alloc op's CIDR (docs/data-model.md §3:
// "typically a /32 or /128 host route") to the bare host address PVE's
// IPAM plugin API expects. cidr that fails to parse as a CIDR (already a
// bare address, or malformed — schema validation already rejects the
// latter before apply ever reaches here) is passed through unchanged.
func allocHostAddr(cidr string) string {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return cidr
	}
	return ip.String()
}

// upidNode extracts the node segment from a PVE UPID
// ("UPID:<node>:<pid>:...").
func upidNode(upid string) string {
	parts := strings.Split(upid, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// --- T-502: firewall op family --------------------------------------------

// fwScope resolves a firewall ruleset Ref (internal/inventory's cluster/
// node/"guest/<kind>/<vmid>"/"vnet/<zone>/<vnet>" ID convention — see
// internal/change/params_fw.go's doc comment; the vnet shape was added by
// T-3103) to the pve.FirewallScope its API calls need.
func (g *pveGateway) fwScope(target inventory.Ref) (pve.FirewallScope, error) {
	switch {
	case target.ID == "cluster":
		return pve.ClusterFirewallScope(), nil
	case target.ID == "node":
		return pve.NodeFirewallScope(target.Node), nil
	case strings.HasPrefix(target.ID, "vnet/"):
		parts := strings.SplitN(target.ID, "/", 3)
		if len(parts) != 3 {
			return pve.FirewallScope{}, fmt.Errorf("pve gateway: unrecognized firewall ruleset target %s", target)
		}
		// parts[1] is the owning zone, kept in the Ref for identity/display
		// but not needed on the wire: real PVE's vnet firewall path is keyed
		// by vnet name alone (vnet ids are unique cluster-wide).
		return pve.VnetFirewallScope(parts[2]), nil
	default:
		parts := strings.SplitN(target.ID, "/", 3)
		if len(parts) != 3 || parts[0] != "guest" {
			return pve.FirewallScope{}, fmt.Errorf("pve gateway: unrecognized firewall ruleset target %s", target)
		}
		vmid, err := strconv.Atoi(parts[2])
		if err != nil {
			return pve.FirewallScope{}, fmt.Errorf("pve gateway: firewall ruleset target %s: invalid vmid: %w", target, err)
		}
		return pve.GuestFirewallScope(target.Node, pve.GuestKind(parts[1]), vmid), nil
	}
}

// fwLogicalScope recovers the inventory.FwScope a firewall ruleset Ref
// names, mirroring internal/change's unexported fwScopeOfRef (same Ref
// convention, params_fw.go's doc comment) — duplicated rather than
// exported/imported since internal/change does not otherwise expose it and
// this package already owns its own fwScope (pve.FirewallScope) resolver
// right above.
func fwLogicalScope(target inventory.Ref) inventory.FwScope {
	switch {
	case target.ID == "cluster":
		return inventory.FwScopeCluster
	case target.ID == "node":
		return inventory.FwScopeNode
	case strings.HasPrefix(target.ID, "vnet/"):
		return inventory.FwScopeVNet
	default:
		return inventory.FwScopeGuest
	}
}

// fwScopeIncludesObjects reports whether scope supports the aliases/ipset
// endpoints at all: real PVE mounts them at cluster and guest scope only
// (T-608's hardware validation), never at node or vnet scope (vnet scope
// hardware-captured in the same file, planning/reports/evidence/
// pve-9.2.4-sdn-schema.txt's "### ls /cluster/sdn/vnets/labnet/firewall":
// only rules+options). captureFwScope/reconcileFwScope use this to skip
// those calls where the endpoint simply does not exist, rather than
// erroring the whole snapshot/restore on a 404.
func fwScopeIncludesObjects(scope inventory.FwScope) bool {
	return scope == inventory.FwScopeCluster || scope == inventory.FwScopeGuest
}

// fwScopeIncludesInOut reports whether scope's options carry policy_in/
// policy_out at all: every scope except vnet does (vnet's options endpoint
// is hardware-captured to expose only enable/policy_forward/
// log_level_forward — planning/reports/evidence/pve-9.2.4-sdn-schema.txt).
// reconcileFwScope uses this so a vnet-scope restore never sends
// policy_in/policy_out (always empty in a vnet snapshot) to an endpoint
// that, per that capture, does not have those fields.
func fwScopeIncludesInOut(scope inventory.FwScope) bool {
	return scope != inventory.FwScopeVNet
}

// FirewallRuleFields implements change.PVEGateway.
func (g *pveGateway) FirewallRuleFields(ctx context.Context, ref inventory.Ref, pos int) (change.FwRuleFields, error) {
	scope, err := g.fwScope(ref)
	if err != nil {
		return change.FwRuleFields{}, err
	}
	rule, err := g.client.GetFirewallRule(ctx, scope, pos)
	if err != nil {
		var reqErr *pve.ErrPVERequest
		if errors.As(err, &reqErr) && reqErr.StatusCode == http.StatusNotFound {
			return change.FwRuleFields{}, &change.ErrFwRuleNotFound{Ref: ref, Pos: pos}
		}
		return change.FwRuleFields{}, err
	}
	return fwRuleFieldsFromPVE(*rule), nil
}

func fwRuleFieldsFromPVE(r pve.FirewallRule) change.FwRuleFields {
	return change.FwRuleFields{
		Direction: r.Type, Action: r.Action, Proto: r.Proto, Source: r.Source, Dest: r.Dest,
		Sport: r.Sport, Dport: r.Dport, Iface: r.Iface, Macro: r.Macro, Log: r.Log,
		Comment: r.Comment, Enabled: r.Enabled,
	}
}

func fwRuleFromSpec(r change.FwRuleSpec) pve.FirewallRule {
	return pve.FirewallRule{
		Type: r.Direction, Action: r.Action, Proto: r.Proto, Source: r.Source, Dest: r.Dest,
		Sport: r.Sport, Dport: r.Dport, Macro: r.Macro, Comment: r.Comment, Enabled: r.Enabled,
	}
}

// ApplyFwOp implements change.PVEGateway: dispatches one fw.* op to the
// concrete PVE API call(s) it realizes as. Object ops (alias/ipset/group)
// with no natural "patch" endpoint (comment-only rename, ipset membership)
// are realized as a read-current + diff + write-the-delta sequence; rule
// creation at a non-appended position is realized as create-then-move
// (see pve.Client.CreateFirewallRule's doc comment) — every one of these
// multi-call realizations is not atomic against a concurrent external
// edit, same as every other multi-step PVE API interaction in this
// codebase (e.g. bridge port add/remove).
func (g *pveGateway) ApplyFwOp(ctx context.Context, op change.Op) error {
	scope, err := g.fwScope(op.Target)
	if err != nil {
		return err
	}
	switch p := op.Params.(type) {
	case *change.FwRuleCreateParams:
		return g.createFwRule(ctx, scope, p)
	case *change.FwRuleUpdateParams:
		return g.updateFwRule(ctx, scope, p)
	case *change.FwRuleDeleteParams:
		return g.client.DeleteFirewallRule(ctx, scope, p.Pos)
	case *change.FwRuleMoveParams:
		return g.moveFwRule(ctx, scope, p)
	case *change.FwOptionsUpdateParams:
		return g.client.UpdateFirewallOptions(ctx, scope, pve.FirewallOptionsUpdate{
			Enable: p.Enabled, PolicyIn: p.DefaultIn, PolicyOut: p.DefaultOut,
			PolicyForward: p.DefaultForward, LogLevelForward: p.LogLevelForward,
		})
	case *change.FwAliasCreateParams:
		return g.client.CreateFirewallAlias(ctx, scope, pve.FirewallAlias{Name: p.Name, CIDR: p.CIDR, Comment: p.Comment})
	case *change.FwAliasUpdateParams:
		return g.updateFwAlias(ctx, scope, p)
	case *change.FwAliasDeleteParams:
		return g.client.DeleteFirewallAlias(ctx, scope, p.Name)
	case *change.FwIpsetCreateParams:
		return g.createFwIpset(ctx, scope, p)
	case *change.FwIpsetUpdateParams:
		return g.updateFwIpset(ctx, scope, p)
	case *change.FwIpsetDeleteParams:
		return g.client.DeleteFirewallIPSet(ctx, scope, p.Name)
	case *change.FwGroupCreateParams:
		return g.createFwGroup(ctx, p)
	case *change.FwGroupUpdateParams:
		return g.updateFwGroup(ctx, p)
	case *change.FwGroupDeleteParams:
		return g.client.DeleteFirewallGroup(ctx, p.Name)
	default:
		return fmt.Errorf("pve gateway: unsupported firewall op params %T", op.Params)
	}
}

func (g *pveGateway) createFwRule(ctx context.Context, scope pve.FirewallScope, p *change.FwRuleCreateParams) error {
	rule := pve.FirewallRule{
		Type: p.Direction, Action: p.Action, Proto: p.Proto, Source: p.Source, Dest: p.Dest,
		Sport: p.Sport, Dport: p.Dport, Iface: p.Iface, Macro: p.Macro, Log: p.Log,
		Comment: p.Comment, Enabled: p.Enabled,
	}
	if err := g.client.CreateFirewallRule(ctx, scope, rule); err != nil {
		return fmt.Errorf("creating firewall rule: %w", err)
	}
	rules, err := g.client.ListFirewallRules(ctx, scope)
	if err != nil {
		return fmt.Errorf("listing rules after create to locate the new rule: %w", err)
	}
	endPos := len(rules) - 1
	if endPos < 0 {
		return fmt.Errorf("pve gateway: created rule not found in ruleset")
	}
	if p.Pos == endPos {
		return nil
	}
	rule.Pos = endPos
	moveTo := p.Pos
	if err := g.client.UpdateFirewallRule(ctx, scope, endPos, rule, &moveTo); err != nil {
		return fmt.Errorf("moving newly created rule to pos %d: %w", p.Pos, err)
	}
	return nil
}

func (g *pveGateway) updateFwRule(ctx context.Context, scope pve.FirewallScope, p *change.FwRuleUpdateParams) error {
	current, err := g.client.GetFirewallRule(ctx, scope, p.Pos)
	if err != nil {
		return fmt.Errorf("reading current rule before update: %w", err)
	}
	merged := *current
	if p.Direction != nil {
		merged.Type = *p.Direction
	}
	if p.Action != nil {
		merged.Action = *p.Action
	}
	if p.Proto != nil {
		merged.Proto = *p.Proto
	}
	if p.Source != nil {
		merged.Source = *p.Source
	}
	if p.Dest != nil {
		merged.Dest = *p.Dest
	}
	if p.Sport != nil {
		merged.Sport = *p.Sport
	}
	if p.Dport != nil {
		merged.Dport = *p.Dport
	}
	if p.Iface != nil {
		merged.Iface = *p.Iface
	}
	if p.Macro != nil {
		merged.Macro = *p.Macro
	}
	if p.Log != nil {
		merged.Log = *p.Log
	}
	if p.Comment != nil {
		merged.Comment = *p.Comment
	}
	if p.Enabled != nil {
		merged.Enabled = *p.Enabled
	}
	if err := g.client.UpdateFirewallRule(ctx, scope, p.Pos, merged, nil); err != nil {
		return fmt.Errorf("updating firewall rule at pos %d: %w", p.Pos, err)
	}
	return nil
}

// moveFwRule realizes fw.rule.move. The apply-time position revalidation
// (acceptance criterion 3) already happened in the executor, against a
// live FirewallRuleFields read, before this method is ever called — this
// re-reads the rule's current content anyway because PVE's moveto call
// still requires resending the rule's full field content unchanged.
func (g *pveGateway) moveFwRule(ctx context.Context, scope pve.FirewallScope, p *change.FwRuleMoveParams) error {
	current, err := g.client.GetFirewallRule(ctx, scope, p.FromPos)
	if err != nil {
		return fmt.Errorf("reading rule before move: %w", err)
	}
	moveTo := p.ToPos
	if err := g.client.UpdateFirewallRule(ctx, scope, p.FromPos, *current, &moveTo); err != nil {
		return fmt.Errorf("moving rule from pos %d to %d: %w", p.FromPos, p.ToPos, err)
	}
	return nil
}

func (g *pveGateway) updateFwAlias(ctx context.Context, scope pve.FirewallScope, p *change.FwAliasUpdateParams) error {
	current, err := g.client.GetFirewallAlias(ctx, scope, p.Name)
	if err != nil {
		return fmt.Errorf("reading current alias before update: %w", err)
	}
	merged := *current
	if p.CIDR != nil {
		merged.CIDR = *p.CIDR
	}
	if p.Comment != nil {
		merged.Comment = *p.Comment
	}
	if err := g.client.UpdateFirewallAlias(ctx, scope, p.Name, merged); err != nil {
		return fmt.Errorf("updating alias %q: %w", p.Name, err)
	}
	return nil
}

func (g *pveGateway) createFwIpset(ctx context.Context, scope pve.FirewallScope, p *change.FwIpsetCreateParams) error {
	if err := g.client.CreateFirewallIPSet(ctx, scope, p.Name, p.Comment); err != nil {
		return fmt.Errorf("creating ipset %q: %w", p.Name, err)
	}
	for _, cidr := range p.CIDRs {
		if err := g.client.CreateFirewallIPSetEntry(ctx, scope, p.Name, pve.FirewallIPSetEntry{CIDR: cidr}); err != nil {
			return fmt.Errorf("adding entry %q to ipset %q: %w", cidr, p.Name, err)
		}
	}
	return nil
}

// updateFwIpset realizes fw.ipset.update: a comment rename (if set) plus a
// membership diff (if CIDRs is set) — add what's missing, remove what's no
// longer wanted — since the PVE ipset API has no "replace all entries" call.
func (g *pveGateway) updateFwIpset(ctx context.Context, scope pve.FirewallScope, p *change.FwIpsetUpdateParams) error {
	if p.Comment != nil {
		if err := g.client.UpdateFirewallIPSet(ctx, scope, p.Name, *p.Comment); err != nil {
			return fmt.Errorf("renaming ipset %q's comment: %w", p.Name, err)
		}
	}
	if p.CIDRs == nil {
		return nil
	}
	current, err := g.client.ListFirewallIPSetEntries(ctx, scope, p.Name)
	if err != nil {
		return fmt.Errorf("listing current entries of ipset %q: %w", p.Name, err)
	}
	want := make(map[string]bool, len(*p.CIDRs))
	for _, c := range *p.CIDRs {
		want[c] = true
	}
	have := make(map[string]bool, len(current))
	for _, e := range current {
		have[e.CIDR] = true
	}
	for cidr := range have {
		if want[cidr] {
			continue
		}
		if err := g.client.DeleteFirewallIPSetEntry(ctx, scope, p.Name, cidr); err != nil {
			return fmt.Errorf("removing entry %q from ipset %q: %w", cidr, p.Name, err)
		}
	}
	for cidr := range want {
		if have[cidr] {
			continue
		}
		if err := g.client.CreateFirewallIPSetEntry(ctx, scope, p.Name, pve.FirewallIPSetEntry{CIDR: cidr}); err != nil {
			return fmt.Errorf("adding entry %q to ipset %q: %w", cidr, p.Name, err)
		}
	}
	return nil
}

func (g *pveGateway) createFwGroup(ctx context.Context, p *change.FwGroupCreateParams) error {
	if err := g.client.CreateFirewallGroup(ctx, p.Name, p.Comment); err != nil {
		return fmt.Errorf("creating security group %q: %w", p.Name, err)
	}
	for _, r := range p.Rules {
		if err := g.client.CreateFirewallGroupRule(ctx, p.Name, fwRuleFromSpec(r)); err != nil {
			return fmt.Errorf("adding rule to security group %q: %w", p.Name, err)
		}
	}
	return nil
}

// updateFwGroup realizes fw.group.update's Rules replacement by deleting
// every existing member rule (highest position first, so the mock/real
// API's renumber-on-delete never shifts an index this loop hasn't visited
// yet) and recreating from p.Rules in order. Comment is not handled here:
// neither pvemock nor the real PVE security-group surface this task wires
// exposes a rename-the-group-itself call distinct from its member-rule
// CRUD — flagged as a narrow, documented gap in the T-502 report.
func (g *pveGateway) updateFwGroup(ctx context.Context, p *change.FwGroupUpdateParams) error {
	if p.Rules == nil {
		return nil
	}
	current, err := g.client.GetFirewallGroupRules(ctx, p.Name)
	if err != nil {
		return fmt.Errorf("reading current rules of security group %q: %w", p.Name, err)
	}
	for i := len(current) - 1; i >= 0; i-- {
		if err := g.client.DeleteFirewallGroupRule(ctx, p.Name, current[i].Pos); err != nil {
			return fmt.Errorf("clearing rule %d of security group %q: %w", current[i].Pos, p.Name, err)
		}
	}
	for _, r := range *p.Rules {
		if err := g.client.CreateFirewallGroupRule(ctx, p.Name, fwRuleFromSpec(r)); err != nil {
			return fmt.Errorf("recreating rule of security group %q: %w", p.Name, err)
		}
	}
	return nil
}

// fwScopeSnapshot is the JSON-serializable form SnapshotFirewallScope/
// RestoreFirewallScope exchange: one ruleset scope's full content.
type fwScopeSnapshot struct {
	Options pve.FirewallOptions `json:"options"`
	Rules   []pve.FirewallRule  `json:"rules"`
	Aliases []pve.FirewallAlias `json:"aliases"`
	IPSets  []fwIPSetSnapshot   `json:"ipsets"`
	Groups  []fwGroupSnapshot   `json:"groups,omitempty"`
}

type fwIPSetSnapshot struct {
	Name    string                   `json:"name"`
	Comment string                   `json:"comment,omitempty"`
	Entries []pve.FirewallIPSetEntry `json:"entries"`
}

type fwGroupSnapshot struct {
	Name    string             `json:"name"`
	Comment string             `json:"comment,omitempty"`
	Rules   []pve.FirewallRule `json:"rules"`
}

// SnapshotFirewallScope implements change.PVEGateway.
func (g *pveGateway) SnapshotFirewallScope(ctx context.Context, ref inventory.Ref) (string, error) {
	scope, err := g.fwScope(ref)
	if err != nil {
		return "", err
	}
	logical := fwLogicalScope(ref)
	snap, err := g.captureFwScope(ctx, scope, logical == inventory.FwScopeCluster, fwScopeIncludesObjects(logical))
	if err != nil {
		return "", fmt.Errorf("capturing firewall scope %s: %w", ref, err)
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return "", fmt.Errorf("marshaling firewall scope snapshot for %s: %w", ref, err)
	}
	return string(b), nil
}

func (g *pveGateway) captureFwScope(ctx context.Context, scope pve.FirewallScope, includeGroups, includeObjects bool) (fwScopeSnapshot, error) {
	var out fwScopeSnapshot
	rules, err := g.client.ListFirewallRules(ctx, scope)
	if err != nil {
		return out, err
	}
	out.Rules = rules
	opts, err := g.client.GetFirewallOptions(ctx, scope)
	if err != nil {
		return out, err
	}
	out.Options = *opts
	if includeObjects {
		aliases, err := g.client.ListFirewallAliases(ctx, scope)
		if err != nil {
			return out, err
		}
		out.Aliases = aliases
		ipsets, err := g.client.ListFirewallIPSets(ctx, scope)
		if err != nil {
			return out, err
		}
		for _, s := range ipsets {
			entries, err := g.client.ListFirewallIPSetEntries(ctx, scope, s.Name)
			if err != nil {
				return out, err
			}
			out.IPSets = append(out.IPSets, fwIPSetSnapshot{Name: s.Name, Comment: s.Comment, Entries: entries})
		}
	}
	if includeGroups {
		groups, err := g.client.ListFirewallGroups(ctx)
		if err != nil {
			return out, err
		}
		for _, gr := range groups {
			grRules, err := g.client.GetFirewallGroupRules(ctx, gr.Name)
			if err != nil {
				return out, err
			}
			out.Groups = append(out.Groups, fwGroupSnapshot{Name: gr.Name, Comment: gr.Comment, Rules: grRules})
		}
	}
	return out, nil
}

// RestoreFirewallScope implements change.PVEGateway: reconciles ref's live
// ruleset back to a captured snapshot by clearing every collection (rules/
// aliases/ipsets/groups) and recreating it from the snapshot, rather than a
// surgical diff — the same "replace the whole thing" model the interfaces-
// file restore uses, adapted to a CRUD API with no whole-file PUT. See
// PVEGateway's doc comment for the scope this is (and isn't) invoked for.
func (g *pveGateway) RestoreFirewallScope(ctx context.Context, ref inventory.Ref, snapshot string) error {
	scope, err := g.fwScope(ref)
	if err != nil {
		return err
	}
	var want fwScopeSnapshot
	if err := json.Unmarshal([]byte(snapshot), &want); err != nil {
		return fmt.Errorf("decoding firewall scope snapshot for %s: %w", ref, err)
	}
	logical := fwLogicalScope(ref)
	if err := g.reconcileFwScope(ctx, scope, logical == inventory.FwScopeCluster, fwScopeIncludesObjects(logical), fwScopeIncludesInOut(logical), want); err != nil {
		return fmt.Errorf("restoring firewall scope %s: %w", ref, err)
	}
	return nil
}

func (g *pveGateway) reconcileFwScope(ctx context.Context, scope pve.FirewallScope, includeGroups, includeObjects, includeInOut bool, want fwScopeSnapshot) error {
	live, err := g.client.ListFirewallRules(ctx, scope)
	if err != nil {
		return fmt.Errorf("listing live rules: %w", err)
	}
	for i := len(live) - 1; i >= 0; i-- {
		if err = g.client.DeleteFirewallRule(ctx, scope, live[i].Pos); err != nil {
			return fmt.Errorf("deleting rule %d: %w", live[i].Pos, err)
		}
	}
	for _, r := range want.Rules {
		if err = g.client.CreateFirewallRule(ctx, scope, r); err != nil {
			return fmt.Errorf("recreating rule: %w", err)
		}
	}

	enable := want.Options.Enable
	upd := pve.FirewallOptionsUpdate{Enable: &enable}
	// PolicyIn/PolicyOut, like PolicyForward/LogLevelForward below, are only
	// restored when the snapshot actually captured a value. An empty string
	// is not a valid policy_in/policy_out and real PVE (9.2.10, T-3202
	// Scenario 5) rejects it with 400 "Parameter verification failed" —
	// a node scope that never had its in/out policy explicitly set (PVE
	// applies its own default without persisting one) reports it as "",
	// and sending that back unconditionally broke every rollback of such a
	// scope, live, on real hardware.
	if includeInOut {
		if want.Options.PolicyIn != "" {
			policyIn := want.Options.PolicyIn
			upd.PolicyIn = &policyIn
		}
		if want.Options.PolicyOut != "" {
			policyOut := want.Options.PolicyOut
			upd.PolicyOut = &policyOut
		}
	}
	if want.Options.PolicyForward != "" {
		pf := want.Options.PolicyForward
		upd.PolicyForward = &pf
	}
	if want.Options.LogLevelForward != "" {
		llf := want.Options.LogLevelForward
		upd.LogLevelForward = &llf
	}
	if err = g.client.UpdateFirewallOptions(ctx, scope, upd); err != nil {
		return fmt.Errorf("restoring options: %w", err)
	}

	if !includeObjects {
		return nil
	}

	liveAliases, err := g.client.ListFirewallAliases(ctx, scope)
	if err != nil {
		return fmt.Errorf("listing live aliases: %w", err)
	}
	for _, a := range liveAliases {
		if err = g.client.DeleteFirewallAlias(ctx, scope, a.Name); err != nil {
			return fmt.Errorf("deleting alias %q: %w", a.Name, err)
		}
	}
	for _, a := range want.Aliases {
		if err = g.client.CreateFirewallAlias(ctx, scope, a); err != nil {
			return fmt.Errorf("recreating alias %q: %w", a.Name, err)
		}
	}

	liveSets, err := g.client.ListFirewallIPSets(ctx, scope)
	if err != nil {
		return fmt.Errorf("listing live ipsets: %w", err)
	}
	for _, s := range liveSets {
		if err = g.client.DeleteFirewallIPSet(ctx, scope, s.Name); err != nil {
			return fmt.Errorf("deleting ipset %q: %w", s.Name, err)
		}
	}
	for _, s := range want.IPSets {
		if err = g.client.CreateFirewallIPSet(ctx, scope, s.Name, s.Comment); err != nil {
			return fmt.Errorf("recreating ipset %q: %w", s.Name, err)
		}
		for _, e := range s.Entries {
			if err = g.client.CreateFirewallIPSetEntry(ctx, scope, s.Name, e); err != nil {
				return fmt.Errorf("recreating ipset %q entry: %w", s.Name, err)
			}
		}
	}

	if !includeGroups {
		return nil
	}
	liveGroups, err := g.client.ListFirewallGroups(ctx)
	if err != nil {
		return fmt.Errorf("listing live groups: %w", err)
	}
	for _, gr := range liveGroups {
		if err = g.client.DeleteFirewallGroup(ctx, gr.Name); err != nil {
			return fmt.Errorf("deleting group %q: %w", gr.Name, err)
		}
	}
	for _, gr := range want.Groups {
		if err = g.client.CreateFirewallGroup(ctx, gr.Name, gr.Comment); err != nil {
			return fmt.Errorf("recreating group %q: %w", gr.Name, err)
		}
		for _, r := range gr.Rules {
			if err = g.client.CreateFirewallGroupRule(ctx, gr.Name, r); err != nil {
				return fmt.Errorf("recreating group %q rule: %w", gr.Name, err)
			}
		}
	}
	return nil
}

// FirewallCompileStatus implements change.PVEGateway.
//
// GET /nodes/{node}/firewall/status does not exist on real PVE (found on a
// real 9.2.4/9.2.10 node, T-3202 — planning/reports/blocked-validation.md
// §2.7): pvesh ls /nodes/{node}/firewall lists only log/options/rules,
// never status. It was modeled from documentation, not a hardware capture
// — this codebase's own repeatedly-learned lesson — and every real
// fw.*-touching changeset's apply hit this as a hard PVE 501 error,
// failing the fw_verify step and rolling back regardless of whether
// pve-firewall actually compiled the change cleanly. A 501 here means
// "this PVE build has no way to answer this question", not "the compile
// failed" — treated as verification-unavailable (OK, with an explanatory
// message) rather than a step failure, the same "degrade, don't block on
// infrastructure you can't observe" choice this codebase already makes
// for e.g. GET /cluster/ceph/config's identical 501 elsewhere. Any other
// error (a real reachability/auth failure) still propagates and still
// fails the step — only the specific "PVE says this route doesn't exist"
// case is treated as inconclusive.
func (g *pveGateway) FirewallCompileStatus(ctx context.Context, node string) (change.FwCompileStatus, error) {
	status, err := g.client.GetFirewallCompileStatus(ctx, node)
	if err != nil {
		var srvErr *pve.ErrPVEServer
		if errors.As(err, &srvErr) && srvErr.StatusCode == http.StatusNotImplemented {
			return change.FwCompileStatus{
				OK:      true,
				Message: "firewall compile-status verification unavailable on this PVE build (GET .../firewall/status not implemented) — apply proceeded unverified",
			}, nil
		}
		return change.FwCompileStatus{}, err
	}
	return change.FwCompileStatus{OK: status.OK(), Message: status.Message}, nil
}

// dhcpAllocationsAdapter adapts *ipam.Service into change.AllocationsSource
// for T-406's DHCP-range-overlap advisory check (validate_advisory.go's
// checkDHCPRangeOverlap in internal/change). internal/change deliberately
// never imports internal/ipam directly — this small adapter, living in
// cmd/vnproxd alongside every other production PVEGateway/seam
// implementation, is the conversion boundary, the same "small interface,
// adapted by the caller" convention this file's pveGateway itself follows
// throughout for internal/pve types.
type dhcpAllocationsAdapter struct{ ipam *ipam.Service }

// DHCPRangeAllocations implements change.AllocationsSource: every current
// SDN subnet's raw PVE-IPAM allocation set (ipam.Service.AllAllocations),
// flattened into change.DHCPRangeAllocation and excluding gateway entries
// (a gateway is never a "DHCP range overlaps this reservation" candidate —
// it isn't a reservation at all).
func (a dhcpAllocationsAdapter) DHCPRangeAllocations(ctx context.Context) ([]change.DHCPRangeAllocation, error) {
	byCIDR, err := a.ipam.AllAllocations(ctx)
	if err != nil {
		return nil, fmt.Errorf("dhcp allocations adapter: %w", err)
	}
	var out []change.DHCPRangeAllocation
	for cidr, allocs := range byCIDR {
		for _, alloc := range allocs {
			if alloc.Gateway {
				continue
			}
			out = append(out, change.DHCPRangeAllocation{
				Subnet: cidr, IP: alloc.IP, Hostname: alloc.Hostname, MAC: alloc.MAC,
			})
		}
	}
	return out, nil
}
