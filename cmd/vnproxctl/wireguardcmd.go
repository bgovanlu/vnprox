// SPDX-License-Identifier: Apache-2.0

// wireguardcmd.go implements `vnproxctl wireguard` (T-4015): a scripted
// on-ramp to the general-purpose WireGuard tunnel surface — GET
// /wireguard/tunnels for reads, and the ordinary wg.tunnel.*/wg.peer.* ops
// (already general and federation-agnostic per internal/change/op.go, T-4015's
// "Repo facts" correction) staged as changesets for writes. Nothing here is
// new backend machinery: every write subcommand builds exactly the op shape
// web/src/wireguard/wgTunnelOps.ts already builds for the general UI panel,
// then does the same `POST /changesets` `vnproxctl remote changesets create`
// does — this file adds no route, no op type, and no second mutation path
// (CLAUDE.md's change-engine invariant).
//
//	vnproxctl wireguard list                              GET /wireguard/tunnels
//	vnproxctl wireguard show <id>                         GET /wireguard/tunnels, filtered
//	vnproxctl wireguard create --node N --id ID --if-name wg0 [...]
//	vnproxctl wireguard update <id> --node N [--listen-port] [--mtu] [--carrier] [--addresses]
//	vnproxctl wireguard delete <id> --node N
//	vnproxctl wireguard peer-add <tunnelId> --node N --public-key KEY [...]
//	vnproxctl wireguard peer-remove <tunnelId> --node N --public-key KEY
//
// **Stage-only, like every other write path in this binary.** create/update/
// delete/peer-add/peer-remove each build ONE op, POST it to /changesets as a
// draft, print the resulting changeset, and stop — reviewing and applying it
// is `vnproxctl remote changesets apply`'s job (or `vnproxctl remote
// changesets diff` first), the same "stage a draft and stop" contract
// speccmd.go's `spec import` documents. There is no `--apply` flag anywhere
// in this file.
//
// **Three states, one definition.** `list`/`show` report each tunnel's
// up/down/unknown verdict via wireguardTunnelState below, which converts the
// documented wire shape into internal/wireguard.ObservedTunnel and asks
// internal/findings.WgTunnelHasFreshHandshake — the SAME function T-3909's
// federation-tunnel health check and the T-4015 frontend
// (web/src/wireguard/wgTunnelState.ts's doc comment) both key off, so this
// CLI can never disagree with either about which tunnels are up. "unknown"
// is reserved for a read that did not resolve — a per-tunnel row this CLI
// prints always came from a successful GET, so it is always up or down; the
// unknown branch exists so the derivation function itself carries the same
// three-state vocabulary as its UI counterpart, and is exercised directly by
// this file's own tests.
//
// **Never a private key.** WireGuardTunnelView (docs/api.md's WireGuard
// section) has no private-key field — the key is generated on the owning
// node at apply time and never leaves it (docs/security.md). wireguardTunnelWire
// below mirrors that documented shape field-for-field, so there is no field
// this file could even accidentally print one through; wireguardcmd_test.go
// asserts this structurally (an unexpected "privateKey" key in a server
// response is silently dropped, never echoed) and behaviorally (no output
// path ever contains operator-supplied secret material either).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/wireguard"
)

// wireguardPeerWire mirrors docs/api.md's WireGuardPeer shape field-for-
// field: the config fields plus the live-polled status half. It carries no
// private key and no preshared key — PresharedKey is write-only on the wg.*
// op side (params_wg.go) and is never part of any read response.
type wireguardPeerWire struct {
	PublicKey         string   `json:"publicKey"`
	Endpoint          string   `json:"endpoint,omitempty"`
	ObservedEndpoint  string   `json:"observedEndpoint,omitempty"`
	AllowedIPs        []string `json:"allowedIps"`
	KeepaliveSec      int      `json:"keepaliveSec,omitempty"`
	LastHandshakeUnix int64    `json:"lastHandshakeUnix,omitempty"`
	RxBytes           int64    `json:"rxBytes"`
	TxBytes           int64    `json:"txBytes"`
	External          bool     `json:"external"`
	EndpointDrifted   bool     `json:"endpointDrifted"`
}

// wireguardTunnelStatusWire mirrors docs/api.md's `status: {interfaceUp,
// peerCount}`.
type wireguardTunnelStatusWire struct {
	PeerCount   int  `json:"peerCount"`
	InterfaceUp bool `json:"interfaceUp"`
}

// wireguardTunnelWire mirrors docs/api.md's WireGuard section's
// `WireGuardTunnel` shape exactly, redefined here rather than imported for
// the same reason changesetWire's doc comment gives (remote_changesets.go):
// this package's only dependency on the wire contract should be the
// documented JSON shape itself. `State` is the one field NOT on the wire —
// it is computed locally by wireguardTunnelState after decode, the same way
// web/src/wireguard/wgTunnelState.ts derives it client-side rather than
// asking the server for a verdict.
type wireguardTunnelWire struct {
	ID         string                    `json:"id"`
	Node       string                    `json:"node"`
	IfName     string                    `json:"ifName"`
	PublicKey  string                    `json:"publicKey"`
	Carrier    string                    `json:"carrier,omitempty"`
	State      string                    `json:"state"`
	Addresses  []string                  `json:"addresses"`
	Peers      []wireguardPeerWire       `json:"peers"`
	Status     wireguardTunnelStatusWire `json:"status"`
	ListenPort int                       `json:"listenPort"`
	MTU        int                       `json:"mtu"`
}

type wireguardTunnelsWire struct {
	Items []wireguardTunnelWire `json:"items"`
}

// wireguardTunnelState is the single "is this tunnel up" verdict every
// caller in this file uses — up/down/unknown, matching
// web/src/wireguard/wgTunnelState.ts's WgTunnelState vocabulary exactly.
// `unavailable` means the read this tunnel came from did not resolve; a
// tunnel decoded from a genuinely successful GET /wireguard/tunnels is
// always up or down, never unknown — collapsing "can't tell" into "down"
// would misreport a local read failure as a broken tunnel (this task's
// brief, verbatim). Reuses internal/findings.WgTunnelHasFreshHandshake
// (health_wireguard.go) — the identical function T-1407's federation tunnel
// health check keys off — rather than a second staleness computation: a
// third Go definition of "is this tunnel up" would be exactly the bug this
// card's brief warns against.
func wireguardTunnelState(t wireguardTunnelWire, unavailable bool, now time.Time) string {
	if unavailable {
		return "unknown"
	}
	if findings.WgTunnelHasFreshHandshake(wireguardObservedFrom(t), t.Node, t.IfName, now) {
		return "up"
	}
	return "down"
}

// wireguardObservedFrom converts one decoded wire tunnel into the single-
// element internal/wireguard.ObservedTunnel slice
// WgTunnelHasFreshHandshake expects, carrying over only what that function
// reads (node, interface name, and each peer's last-handshake time) — never
// a private key, which the wire shape does not have to carry over in the
// first place.
func wireguardObservedFrom(t wireguardTunnelWire) []wireguard.ObservedTunnel {
	peers := make([]wireguard.ObservedPeer, len(t.Peers))
	for i, p := range t.Peers {
		op := wireguard.ObservedPeer{PublicKey: p.PublicKey}
		if p.LastHandshakeUnix > 0 {
			op.LastHandshake = time.Unix(p.LastHandshakeUnix, 0)
		}
		peers[i] = op
	}
	return []wireguard.ObservedTunnel{{Node: t.Node, IfName: t.IfName, Peers: peers, PublicKey: t.PublicKey, ListenPort: t.ListenPort}}
}

func runWireguard(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl wireguard: expected a subcommand (list, show, create, update, delete, peer-add, peer-remove)")
		return ExitUsage
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return runWireguardList(rest, stdout, stderr)
	case "show":
		return runWireguardShow(rest, stdout, stderr)
	case "create":
		return runWireguardCreate(rest, stdout, stderr)
	case "update":
		return runWireguardUpdate(rest, stdout, stderr)
	case "delete":
		return runWireguardDelete(rest, stdout, stderr)
	case "peer-add":
		return runWireguardPeerAdd(rest, stdout, stderr)
	case "peer-remove":
		return runWireguardPeerRemove(rest, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "vnproxctl wireguard: unknown subcommand %q\n", sub)
		return ExitUsage
	}
}

// --- list / show ------------------------------------------------------

// fetchWireguardTunnels does the one GET /wireguard/tunnels call list and
// show both need, stamping each item's computed State.
func fetchWireguardTunnels(ctx context.Context, client *remoteClient) (wireguardTunnelsWire, int, *apiError, error) {
	var out wireguardTunnelsWire
	status, apiErr, err := client.doJSON(ctx, "GET", "/wireguard/tunnels", nil, &out)
	if err == nil && apiErr == nil {
		now := time.Now()
		for i := range out.Items {
			out.Items[i].State = wireguardTunnelState(out.Items[i], false, now)
		}
	}
	return out, status, apiErr, err
}

func runWireguardList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl wireguard list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, "vnproxctl wireguard list", stderr)
	if !ok {
		return code
	}
	client, code := buildRemoteClient(rf, "vnproxctl wireguard list", stderr)
	if client == nil {
		return code
	}

	ctx, cancel := context.WithTimeout(context.Background(), *rf.timeout)
	defer cancel()
	out, httpStatus, apiErr, err := fetchWireguardTunnels(ctx, client)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl wireguard list: %v\n", err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl wireguard list: %s: %s\n", apiErr.Code, apiErr.Message)
		return exitForAPIError(httpStatus)
	}

	if jsonOut {
		if err := writeJSONOut(stdout, out); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl wireguard list: %v\n", err)
			return ExitError
		}
		return ExitSuccess
	}
	if len(out.Items) == 0 {
		_, _ = fmt.Fprintln(stdout, "No WireGuard tunnels.")
		return ExitSuccess
	}
	_, _ = fmt.Fprintf(stdout, "%-24s  %-12s  %-8s  %-6s  %-5s  %s\n", "ID", "NODE", "IFNAME", "STATE", "PEERS", "ADDRESSES")
	for _, t := range out.Items {
		_, _ = fmt.Fprintf(stdout, "%-24s  %-12s  %-8s  %-6s  %-5d  %s\n", t.ID, t.Node, t.IfName, t.State, t.Status.PeerCount, strings.Join(t.Addresses, ","))
	}
	return ExitSuccess
}

func runWireguardShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl wireguard show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl wireguard show: expected exactly one tunnel id")
		return ExitUsage
	}
	id := fs.Arg(0)
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, "vnproxctl wireguard show", stderr)
	if !ok {
		return code
	}
	client, code := buildRemoteClient(rf, "vnproxctl wireguard show", stderr)
	if client == nil {
		return code
	}

	ctx, cancel := context.WithTimeout(context.Background(), *rf.timeout)
	defer cancel()
	out, httpStatus, apiErr, err := fetchWireguardTunnels(ctx, client)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl wireguard show: %v\n", err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl wireguard show: %s: %s\n", apiErr.Code, apiErr.Message)
		return exitForAPIError(httpStatus)
	}

	var found *wireguardTunnelWire
	for i := range out.Items {
		if out.Items[i].ID == id {
			found = &out.Items[i]
			break
		}
	}
	if found == nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl wireguard show: no such WireGuard tunnel %q\n", id)
		return ExitError
	}

	if jsonOut {
		if err := writeJSONOut(stdout, *found); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl wireguard show: %v\n", err)
			return ExitError
		}
		return ExitSuccess
	}
	printWireguardTunnel(stdout, *found)
	return ExitSuccess
}

func printWireguardTunnel(w io.Writer, t wireguardTunnelWire) {
	_, _ = fmt.Fprintf(w, "ID:          %s\n", t.ID)
	_, _ = fmt.Fprintf(w, "Node:        %s\n", t.Node)
	_, _ = fmt.Fprintf(w, "Interface:   %s\n", t.IfName)
	_, _ = fmt.Fprintf(w, "State:       %s\n", t.State)
	_, _ = fmt.Fprintf(w, "Public key:  %s\n", t.PublicKey)
	if t.Carrier != "" {
		_, _ = fmt.Fprintf(w, "Carrier:     %s\n", t.Carrier)
	}
	_, _ = fmt.Fprintf(w, "Addresses:   %s\n", strings.Join(t.Addresses, ", "))
	_, _ = fmt.Fprintf(w, "Listen port: %d\n", t.ListenPort)
	if t.MTU > 0 {
		_, _ = fmt.Fprintf(w, "MTU:         %d\n", t.MTU)
	}
	_, _ = fmt.Fprintf(w, "Interface up: %t\n", t.Status.InterfaceUp)
	if len(t.Peers) == 0 {
		_, _ = fmt.Fprintln(w, "Peers:       none")
		return
	}
	_, _ = fmt.Fprintln(w, "Peers:")
	for _, p := range t.Peers {
		handshake := "never"
		if p.LastHandshakeUnix > 0 {
			handshake = strconvItoa64(p.LastHandshakeUnix)
		}
		_, _ = fmt.Fprintf(w, "  %s  endpoint=%s  allowedIps=%s  external=%t  drifted=%t  lastHandshakeUnix=%s\n",
			p.PublicKey, p.Endpoint, strings.Join(p.AllowedIPs, ","), p.External, p.EndpointDrifted, handshake)
	}
}

// --- create / update / delete / peer-add / peer-remove -----------------

// createChangesetBody mirrors docs/api.md's `POST /changesets` body
// (`{title, ops}`) exactly like remote_changesets.go's create path, except
// here `ops` is a real `[]change.Op` this file constructs itself (rather
// than a caller-supplied file passed through as raw JSON) — `change.Op`'s
// own MarshalJSON (op.go) produces the documented `{op, target, params}`
// envelope, so this struct needs no bespoke encoding of its own.
type createChangesetBody struct {
	Title string      `json:"title"`
	Ops   []change.Op `json:"ops"`
}

// stageWireguardOp POSTs body to /changesets, prints the resulting draft
// changeset (table or -o json), and returns — the identical "stage and
// stop" shape runSpecImport (speccmd.go) uses, never calling anything past
// this one POST.
func stageWireguardOp(cmdName string, rf *remoteFlags, jsonOut bool, body createChangesetBody, stdout, stderr io.Writer) int {
	client, code := buildRemoteClient(rf, cmdName, stderr)
	if client == nil {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), *rf.timeout)
	defer cancel()
	var out changesetWire
	httpStatus, apiErr, err := client.doJSON(ctx, "POST", "/changesets", body, &out)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", cmdName, err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %s: %s\n", cmdName, apiErr.Code, apiErr.Message)
		return exitForAPIError(httpStatus)
	}

	if jsonOut {
		if err := writeJSONOut(stdout, out); err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: %v\n", cmdName, err)
			return ExitError
		}
		return ExitSuccess
	}
	printChangesetTable(stdout, out)
	_, _ = fmt.Fprintf(stdout, "\nStaged as a %s changeset. Nothing was applied — review with `vnproxctl remote changesets diff %s`, then apply with `vnproxctl remote changesets apply %s`.\n",
		out.Status, out.ID, out.ID)
	return ExitSuccess
}

func wgTunnelRef(node, id string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindWgTunnel, Node: node, ID: id}
}

func wgPeerRef(node, tunnelID, publicKey string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindWgPeer, Node: node, ID: tunnelID + "/" + publicKey}
}

func runWireguardCreate(args []string, stdout, stderr io.Writer) int {
	const cmdName = "vnproxctl wireguard create"
	fs := flag.NewFlagSet(cmdName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	node := fs.String("node", "", "owning PVE node (required)")
	id := fs.String("id", "", "caller-chosen tunnel id, unique on --node (required)")
	ifName := fs.String("if-name", "", "on-node WireGuard interface name, e.g. wg0 (required)")
	carrier := fs.String("carrier", "", "underlying interface the tunnel's endpoint rides on, e.g. vmbr0 (mgmt-path carrier declaration)")
	addresses := fs.String("addresses", "", "comma-separated tunnel addresses, CIDR form, e.g. 10.10.0.1/24")
	listenPort := fs.Int("listen-port", 0, "UDP listen port (0 lets the interface pick)")
	mtu := fs.Int("mtu", 0, "MTU (0 uses the interface default)")
	title := fs.String("title", "", "changeset title (default: a generated description)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, cmdName, stderr)
	if !ok {
		return code
	}
	if *node == "" || *id == "" || *ifName == "" {
		_, _ = fmt.Fprintln(stderr, cmdName+": --node, --id and --if-name are required")
		return ExitUsage
	}

	params := &change.WgTunnelCreateParams{IfName: *ifName, Carrier: *carrier, ListenPort: *listenPort, MTU: *mtu}
	if *addresses != "" {
		params.Addresses = splitCommaList(*addresses)
	}
	op := change.Op{Type: change.OpWgTunnelCreate, Target: wgTunnelRef(*node, *id), Params: params}

	t := *title
	if t == "" {
		t = fmt.Sprintf("Create WireGuard tunnel %s on %s", *id, *node)
	}
	return stageWireguardOp(cmdName, rf, jsonOut, createChangesetBody{Title: t, Ops: []change.Op{op}}, stdout, stderr)
}

func runWireguardUpdate(args []string, stdout, stderr io.Writer) int {
	const cmdName = "vnproxctl wireguard update"
	fs := flag.NewFlagSet(cmdName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	node := fs.String("node", "", "owning PVE node (required)")
	carrier := fs.String("carrier", "", "new carrier interface")
	addresses := fs.String("addresses", "", "new comma-separated tunnel addresses")
	listenPort := fs.Int("listen-port", 0, "new UDP listen port")
	mtu := fs.Int("mtu", 0, "new MTU")
	title := fs.String("title", "", "changeset title (default: a generated description)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, cmdName+": expected exactly one tunnel id")
		return ExitUsage
	}
	id := fs.Arg(0)
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, cmdName, stderr)
	if !ok {
		return code
	}
	if *node == "" {
		_, _ = fmt.Fprintln(stderr, cmdName+": --node is required")
		return ExitUsage
	}

	// Only flags the operator actually passed become non-nil pointer
	// fields (WgTunnelUpdateParams: nil == leave unchanged) — flag.Visit
	// reports exactly the flags set on this invocation's command line, not
	// every flag's zero-valued default, which is what "only what changed"
	// requires here without a pre-fetched "initial" value to diff against
	// the way the frontend's buildWgTunnelUpdateOp does.
	var params change.WgTunnelUpdateParams
	var setAny bool
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "listen-port":
			v := *listenPort
			params.ListenPort = &v
			setAny = true
		case "mtu":
			v := *mtu
			params.MTU = &v
			setAny = true
		case "carrier":
			v := *carrier
			params.Carrier = &v
			setAny = true
		case "addresses":
			v := splitCommaList(*addresses)
			params.Addresses = &v
			setAny = true
		}
	})
	if !setAny {
		_, _ = fmt.Fprintln(stderr, cmdName+": at least one of --listen-port, --mtu, --carrier, --addresses is required")
		return ExitUsage
	}

	op := change.Op{Type: change.OpWgTunnelUpdate, Target: wgTunnelRef(*node, id), Params: &params}
	t := *title
	if t == "" {
		t = fmt.Sprintf("Update WireGuard tunnel %s on %s", id, *node)
	}
	return stageWireguardOp(cmdName, rf, jsonOut, createChangesetBody{Title: t, Ops: []change.Op{op}}, stdout, stderr)
}

func runWireguardDelete(args []string, stdout, stderr io.Writer) int {
	const cmdName = "vnproxctl wireguard delete"
	fs := flag.NewFlagSet(cmdName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	node := fs.String("node", "", "owning PVE node (required)")
	title := fs.String("title", "", "changeset title (default: a generated description)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, cmdName+": expected exactly one tunnel id")
		return ExitUsage
	}
	id := fs.Arg(0)
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, cmdName, stderr)
	if !ok {
		return code
	}
	if *node == "" {
		_, _ = fmt.Fprintln(stderr, cmdName+": --node is required")
		return ExitUsage
	}

	op := change.Op{Type: change.OpWgTunnelDelete, Target: wgTunnelRef(*node, id), Params: &change.WgTunnelDeleteParams{}}
	t := *title
	if t == "" {
		t = fmt.Sprintf("Delete WireGuard tunnel %s on %s", id, *node)
	}
	return stageWireguardOp(cmdName, rf, jsonOut, createChangesetBody{Title: t, Ops: []change.Op{op}}, stdout, stderr)
}

func runWireguardPeerAdd(args []string, stdout, stderr io.Writer) int {
	const cmdName = "vnproxctl wireguard peer-add"
	fs := flag.NewFlagSet(cmdName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	node := fs.String("node", "", "owning PVE node (required)")
	publicKey := fs.String("public-key", "", "the peer's WireGuard public key (required) — re-submitting an existing key edits that peer in place (AddPeer is an upsert)")
	endpoint := fs.String("endpoint", "", "the peer's endpoint host:port (\"\" is valid — a road-warrior peer with no fixed address)")
	allowedIPs := fs.String("allowed-ips", "", "comma-separated allowed IPs/CIDRs")
	presharedKey := fs.String("preshared-key", "", "optional PSK — sealed at stage time, never echoed back by any read (params_wg.go)")
	keepaliveSec := fs.Int("keepalive-sec", 0, "optional persistent keepalive, seconds")
	clusterID := fs.String("cluster-id", "", "optional attached-federation-cluster tag for this peer")
	title := fs.String("title", "", "changeset title (default: a generated description)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, cmdName+": expected exactly one tunnel id")
		return ExitUsage
	}
	tunnelID := fs.Arg(0)
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, cmdName, stderr)
	if !ok {
		return code
	}
	if *node == "" || *publicKey == "" {
		_, _ = fmt.Fprintln(stderr, cmdName+": --node and --public-key are required")
		return ExitUsage
	}

	params := &change.WgPeerAddParams{
		PublicKey:    *publicKey,
		Endpoint:     *endpoint,
		PresharedKey: *presharedKey,
		ClusterID:    *clusterID,
		KeepaliveSec: *keepaliveSec,
		External:     true, // the far side is never vnprox's own to apply against — wizardOps.ts's shared convention
	}
	if *allowedIPs != "" {
		params.AllowedIPs = splitCommaList(*allowedIPs)
	}
	op := change.Op{Type: change.OpWgPeerAdd, Target: wgPeerRef(*node, tunnelID, *publicKey), Params: params}

	t := *title
	if t == "" {
		t = fmt.Sprintf("Add WireGuard peer to tunnel %s on %s", tunnelID, *node)
	}
	return stageWireguardOp(cmdName, rf, jsonOut, createChangesetBody{Title: t, Ops: []change.Op{op}}, stdout, stderr)
}

func runWireguardPeerRemove(args []string, stdout, stderr io.Writer) int {
	const cmdName = "vnproxctl wireguard peer-remove"
	fs := flag.NewFlagSet(cmdName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	node := fs.String("node", "", "owning PVE node (required)")
	publicKey := fs.String("public-key", "", "the peer's WireGuard public key to remove (required)")
	title := fs.String("title", "", "changeset title (default: a generated description)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, cmdName+": expected exactly one tunnel id")
		return ExitUsage
	}
	tunnelID := fs.Arg(0)
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, cmdName, stderr)
	if !ok {
		return code
	}
	if *node == "" || *publicKey == "" {
		_, _ = fmt.Fprintln(stderr, cmdName+": --node and --public-key are required")
		return ExitUsage
	}

	op := change.Op{Type: change.OpWgPeerRemove, Target: wgPeerRef(*node, tunnelID, *publicKey), Params: &change.WgPeerRemoveParams{PublicKey: *publicKey}}
	t := *title
	if t == "" {
		t = fmt.Sprintf("Remove WireGuard peer from tunnel %s on %s", tunnelID, *node)
	}
	return stageWireguardOp(cmdName, rf, jsonOut, createChangesetBody{Title: t, Ops: []change.Op{op}}, stdout, stderr)
}
