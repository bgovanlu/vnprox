package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bgovanlu/vnprox/internal/demo"
	"github.com/bgovanlu/vnprox/internal/publicdemo"
)

// T-2802: `vnproxd --demo --public-demo`.
//
// The flag pair is checked in main.go (a public demo must be a demo). This
// file is only the wiring: it builds the edge with the demo fixture's own
// built-in superuser as the credential every visitor session is minted
// from, and shouts about it in the log.
//
// There is deliberately no config key. --public-demo is a property of how
// the process was started, exactly like --demo (see config.Config.Demo's
// own comment): a config file that could silently turn a real daemon into
// a public one is a footgun with no upside, and a public instance is
// started by whoever wrote the unit file, not by whoever last edited the
// TOML.

func publicDemoEdge(next http.Handler, logger *slog.Logger) (http.Handler, error) {
	// demo.TicketUsername is "root@pam"; the login route takes the user and
	// the realm separately.
	username, realm, found := strings.Cut(demo.TicketUsername, "@")
	if !found {
		return nil, fmt.Errorf("public demo: the demo ticket username %q has no realm", demo.TicketUsername)
	}

	edge, err := publicdemo.New(next, publicdemo.Options{
		Login: publicdemo.Login{
			Username: username,
			Password: demo.TicketPassword,
			Realm:    realm,
		},
		Caps:   publicdemo.DefaultCaps(),
		Logger: logger,
	})
	if err != nil {
		return nil, fmt.Errorf("building the public demo edge: %w", err)
	}

	caps := edge.Caps()
	logger.Warn("PUBLIC DEMO: every mutating route is refused at the edge with 403 before it reaches this daemon. "+
		"Each visitor gets their own session, minted here rather than typed in, and their own resource budget.",
		"max_visitors", caps.MaxVisitors,
		"request_burst_per_visitor", caps.RequestBurst,
		"request_refill", caps.RequestRefill,
		"state_bytes_per_visitor", caps.MaxStateBytes,
		"visitor_idle_ttl", caps.VisitorIdleTTL)

	return edge, nil
}
