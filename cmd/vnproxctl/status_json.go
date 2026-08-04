// status_json.go implements `vnproxctl status -o json` (T-1105's "-o json
// on EVERY command, retrofit the existing three too"). It deliberately does
// not touch runStatus's existing table-rendering code path (status.go) at
// all — it re-probes the same three things (local health endpoint, PVE API,
// peers) through their own small, data-returning helpers, so the pre-
// existing text output and its exit-code logic are provably unchanged
// (main_test.go's regression test pins this).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/peer"
)

//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape, not memory packing.
type statusJSONResult struct {
	PVE        pveHealthJSON  `json:"pve"`
	Endpoint   string         `json:"endpoint"`
	HTTPStatus string         `json:"httpStatus,omitempty"`
	FetchError string         `json:"fetchError,omitempty"`
	Daemon     healthResponse `json:"daemon"`
	Peers      []peerJSON     `json:"peers"`
	LatencyMS  int64          `json:"latencyMs"`
	Reachable  bool           `json:"reachable"`
}

type pveHealthJSON struct {
	Error      string `json:"error,omitempty"`
	Configured bool   `json:"configured"`
	Reachable  bool   `json:"reachable"`
	NodeCount  int    `json:"nodeCount,omitempty"`
}

//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape, not memory packing.
type peerJSON struct {
	Node            string `json:"node"`
	Addr            string `json:"addr"`
	Error           string `json:"error,omitempty"`
	Version         string `json:"version,omitempty"`
	Reachable       bool   `json:"reachable"`
	ProtocolVersion int    `json:"protocolVersion,omitempty"`
	Incompatible    bool   `json:"incompatible,omitempty"`
}

// runStatusJSON assembles and prints the `-o json` equivalent of runStatus's
// table output, using exactly the same exit-code rule (0 iff the local
// daemon answered 200 with `{"status":"ok"}` — PVE/peer probes never affect
// it, matching the table path).
func runStatusJSON(stdout io.Writer, endpoint string, client *http.Client, cfg *config.Config, cfgErr error, timeout time.Duration) int {
	result := statusJSONResult{Endpoint: endpoint}
	exitCode := 0

	start := time.Now()
	resp, err := client.Get(endpoint)
	result.LatencyMS = time.Since(start).Milliseconds()
	switch {
	case err != nil:
		result.FetchError = err.Error()
		exitCode = 1
	default:
		func() {
			defer func() { _ = resp.Body.Close() }()
			result.Reachable = true
			result.HTTPStatus = resp.Status
			if decodeErr := json.NewDecoder(resp.Body).Decode(&result.Daemon); decodeErr != nil {
				result.FetchError = decodeErr.Error()
				exitCode = 1
				return
			}
			if resp.StatusCode != http.StatusOK || result.Daemon.Status != "ok" {
				exitCode = 1
			}
		}()
	}

	result.PVE = probePVEHealthJSON(cfg, cfgErr)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	result.Peers = probePeersJSON(ctx, cfg, cfgErr, timeout)

	if encErr := writeJSONOut(stdout, result); encErr != nil {
		_, _ = fmt.Fprintf(os.Stderr, "vnproxctl status: %v\n", encErr)
		return 1
	}
	return exitCode
}

// probePVEHealthJSON is printPVEHealth's data-returning twin (status.go).
func probePVEHealthJSON(cfg *config.Config, cfgErr error) pveHealthJSON {
	if cfgErr != nil {
		return pveHealthJSON{Error: fmt.Sprintf("could not load config: %v", cfgErr)}
	}
	client, err := buildStatusPVEClient(cfg)
	if err != nil {
		return pveHealthJSON{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	nodes, err := client.ClusterStatus(ctx)
	if err != nil {
		return pveHealthJSON{Configured: true, Error: err.Error()}
	}
	count := 0
	for _, n := range nodes {
		if n.Type == "node" {
			count++
		}
	}
	return pveHealthJSON{Configured: true, Reachable: true, NodeCount: count}
}

// probePeersJSON is printPeerReachability/printPeerStatuses' data-returning
// twin (status.go).
func probePeersJSON(ctx context.Context, cfg *config.Config, cfgErr error, timeout time.Duration) []peerJSON {
	if cfgErr != nil {
		return nil
	}
	if _, err := os.Stat(cfg.Peer.SecretPath); err != nil {
		return nil
	}
	secrets, err := peer.LoadOrGenerateSecret(cfg.Peer.SecretPath, discardLogger())
	if err != nil {
		return nil
	}
	pveClient, err := buildStatusPVEClient(cfg)
	if err != nil {
		return nil
	}

	port := peer.DefaultPort
	if _, portStr, splitErr := net.SplitHostPort(cfg.Server.Listen); splitErr == nil {
		if p, convErr := strconv.Atoi(portStr); convErr == nil && p > 0 {
			port = p
		}
	}
	trust, err := statusPeerTrust(cfg)
	if err != nil {
		return nil
	}
	peerClient := peer.NewClient(peer.ClientOptions{
		ClusterStatus:  pveClient,
		Secrets:        secrets,
		Port:           port,
		RequestTimeout: timeout,
		Trust:          trust,
		Logger:         discardLogger(),
	})
	peers, err := peerClient.Peers(ctx)
	if err != nil || len(peers) == 0 {
		return nil
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].Node < peers[j].Node })

	out := make([]peerJSON, 0, len(peers))
	for _, p := range peers {
		pj := peerJSON{Node: p.Node, Addr: p.Addr}
		if healthErr := peerClient.Health(ctx, p); healthErr != nil {
			pj.Error = healthErr.Error()
			out = append(out, pj)
			continue
		}
		pj.Reachable = true
		v, verErr := peerClient.Version(ctx, p)
		if verErr != nil {
			pj.Error = verErr.Error()
			out = append(out, pj)
			continue
		}
		pj.Version = v.Version
		pj.ProtocolVersion = v.ProtocolVersion
		pj.Incompatible = v.ProtocolVersion != peer.ProtocolVersion
		out = append(out, pj)
	}
	return out
}
