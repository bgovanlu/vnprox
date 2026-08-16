package verify

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// checks_pwa.go is status-matrix row 73 ("Mobile PWA + push"). The row is
// HW-blocked because the two halves nothing on this machine can prove —
// installing on a real phone and delivering one push through FCM/APNs/
// autopush — need hardware this project does not have. But the failure that
// actually shipped in v4.0.0 was neither of those: the daemon's own CSP
// (`worker-src 'none'; manifest-src 'none'`) refused the service worker and
// the manifest in ANY production browser, and no gate looked. This check is
// that gate at the deployment: it asserts the daemon SERVES an installable
// PWA — manifest reachable with the right media type, sw.js reachable, CSP
// admitting both — which is the entire machine-checkable half of the row.

// RootProbe is an optional capability a DaemonProbe may additionally
// satisfy: fetching a path at the daemon's root (outside /api/v1), with
// response headers. The PWA check needs it because manifest.webmanifest,
// sw.js, and the CSP header all live on the SPA surface, not the API. Same
// structural-seam pattern as change.PeerCompatibilityChecker — a probe that
// cannot do this simply isn't asked, and the check skips with the reason.
type RootProbe interface {
	GetRoot(ctx context.Context, path string) (status int, header http.Header, body []byte, err error)
}

// checkPWAServable asserts the daemon serves an installable PWA: the
// manifest and service worker resolve, and the CSP on the app shell admits
// them ('self' in worker-src/manifest-src). This is exactly the defect
// class T-2901 fixed; a regression to `worker-src 'none'` fails here on a
// live deployment rather than in a field report.
func checkPWAServable(ctx context.Context, d Deps) Outcome {
	// Deps.Root first: all three fetches below are anonymous, so this check
	// must not need a bearer token. Falling back to Daemon keeps existing
	// callers (and the fixtures) working when they only wire that one.
	rp := d.Root
	if rp == nil {
		if d.Daemon == nil {
			return Skip("no daemon URL was available, so the app shell, manifest, and service worker could not be fetched; point --url at this node's daemon (no token is needed — every path this check reads is unauthenticated)")
		}
		var ok bool
		rp, ok = d.Daemon.(RootProbe)
		if !ok {
			return Skip("this daemon probe cannot fetch non-API paths (no RootProbe capability); run through vnproxctl verify against a live daemon")
		}
	}

	status, header, _, err := rp.GetRoot(ctx, "/")
	if err != nil {
		return Fail(fmt.Sprintf("could not fetch the app shell: %v", err), NewEvidence(SourceDaemonAPI, "GET /", err.Error()))
	}
	csp := header.Get("Content-Security-Policy")
	shellEv := NewEvidence(SourceDaemonAPI, "GET /", fmt.Sprintf("status %d, Content-Security-Policy: %s", status, csp))
	var broken []string
	if status != http.StatusOK {
		broken = append(broken, fmt.Sprintf("app shell returned %d", status))
	}
	for _, dir := range []string{"worker-src", "manifest-src"} {
		if !cspDirectiveAllowsSelf(csp, dir) {
			broken = append(broken, fmt.Sprintf("CSP %s does not allow 'self' — a production browser will refuse the %s (the v4.0.0 defect T-2901 fixed)", dir,
				map[string]string{"worker-src": "service worker", "manifest-src": "manifest"}[dir]))
		}
	}

	status, header, _, err = rp.GetRoot(ctx, "/manifest.webmanifest")
	if err != nil {
		return Fail(fmt.Sprintf("could not fetch the web app manifest: %v", err), shellEv, NewEvidence(SourceDaemonAPI, "GET /manifest.webmanifest", err.Error()))
	}
	manifestEv := NewEvidence(SourceDaemonAPI, "GET /manifest.webmanifest", fmt.Sprintf("status %d, Content-Type: %s", status, header.Get("Content-Type")))
	if status != http.StatusOK {
		broken = append(broken, fmt.Sprintf("manifest returned %d", status))
	} else if !strings.Contains(header.Get("Content-Type"), "application/manifest+json") {
		broken = append(broken, fmt.Sprintf("manifest served as %q, not application/manifest+json — Chromium refuses to treat it as installable", header.Get("Content-Type")))
	}

	status, _, _, err = rp.GetRoot(ctx, "/sw.js")
	if err != nil {
		return Fail(fmt.Sprintf("could not fetch the service worker: %v", err), shellEv, manifestEv, NewEvidence(SourceDaemonAPI, "GET /sw.js", err.Error()))
	}
	swEv := NewEvidence(SourceDaemonAPI, "GET /sw.js", fmt.Sprintf("status %d", status))
	if status != http.StatusOK {
		broken = append(broken, fmt.Sprintf("service worker script returned %d", status))
	}

	if len(broken) > 0 {
		return Fail(fmt.Sprintf("the daemon does not serve an installable PWA: %s", strings.Join(broken, "; ")), shellEv, manifestEv, swEv)
	}
	return Pass("manifest, service worker, and CSP all admit the installable PWA; what remains for this row is the on-device half (install on real iOS/Android, one push through a real push service) — planning/reports/needs-hardware-validation.md §T-2901", shellEv, manifestEv, swEv)
}

// cspDirectiveAllowsSelf reports whether csp's named directive exists and
// includes 'self'. An absent directive falls back to default-src; this
// daemon always pins both directives explicitly (middleware.go's cspPolicy),
// so absence is treated as the fallback it is: check default-src instead.
func cspDirectiveAllowsSelf(csp, directive string) bool {
	found := false
	for _, part := range strings.Split(csp, ";") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 || fields[0] != directive {
			continue
		}
		found = true
		for _, src := range fields[1:] {
			if src == "'self'" {
				return true
			}
		}
	}
	if !found && directive != "default-src" {
		return cspDirectiveAllowsSelf(csp, "default-src")
	}
	return false
}
