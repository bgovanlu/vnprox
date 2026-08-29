# T-4304 — The map gets a quarter of the screen, and the error text gets more

**Phase:** 43 (Canvas rendering)
**Status:** deliverables 1, 2 and 4 done; **3 is blocked on an API-contract decision** — see below
**Found by:** the first visual-gate capture of the graph view against a current build (T-4216)

## What the screenshot shows

At the gate's 1400x900 viewport, on `/topology` in graph view, measured off the capture:

| band | y-range | height | share |
|---|---|---|---|
| page title | 75–115 | 40px | 4% |
| layer toggle bar | 118–150 | 32px | 4% |
| action toolbar | 163–195 | 32px | 4% |
| history slider | 210–270 | 60px | 7% |
| **stale-data banner** | 285–435 | **150px** | **17%** |
| **untied-findings banner** | 443–592 | **149px** | **17%** |
| LLDP notice | 605–653 | 48px | 5% |
| **the map** | 665–900 | **235px** | **26%** |

**Three stacked notice banners take 347px — 39% of the viewport — and the map gets 26%.** In the
visible strip of canvas exactly one node is legible.

This is a visual networking tool. Its visual is the smallest region on its own page.

Two caveats stated up front, because they bound the claim rather than dissolve it. The banner
*content* is environment-specific: the mock cluster's peers genuinely are unreachable and its
certificates genuinely are missing, so a healthy cluster shows fewer banners. And a taller display
gives the map more room in absolute terms. Neither changes the ordering — the banners are laid out
above the map and push it down by however much they occupy, on every viewport, whenever they
appear. A cluster with real problems is exactly when an operator most needs the map, and it is
precisely then that the map is smallest.

## The errors are raw Go strings

The stale-data banner renders, verbatim:

> host (node pve2): no successful poll yet — host links (pve2): peer: pve2: peer_untrusted
> (peer_unreachable): circuit open after a certificate verification failure: peer: pve2:
> peer_untrusted: certificate verification failed, treating the peer as unreachable
> (peer_unreachable): Get "https://10.10.0.12:8007/api/peer/host/neighbors?node=pve2": peer:
> peer_untrusted: cluster CA trust anchor unavailable: reading /etc/pve/pve-root-ca.pem: open
> /etc/pve/pve-root-ca.pem: no such file or directory

That is `err.Error()` on a five-level `fmt.Errorf("...: %w")` chain, printed at the operator. The
repo's own convention — *"errors are wrapped with context; no bare `err` returns across package
boundaries"* — is what produces the chain, and it is the right convention **for logs**. A UI
surface is not a log.

Note the contrast with the banner directly below it, which gets this right: *"cluster member pve1
has no certificate at nodes/pve1/pve-ssl.pem — peers cannot verify it and it cannot verify them.
To fix: on pve1: pvecm updatecerts -f"*. Cause, consequence, command. The same underlying
condition, written for a person. So the product already knows how to do this; the peer-poll path
just does not.

## Why nobody had seen it

The visual gate had never run (T-4216 part 1), and when it first ran it captured the switch view
rather than the graph view (part 2), and when that was fixed it captured a three-day-old binary
(the staleness bug fixed in the same commit). Three separate reasons the product's main screen had
no picture taken of it. That is the finding behind the finding: **this is what a gate that has
never produced an artifact costs**, and it is worth remembering the next time one is written and
declared done.

## Result

`NoticeStack` collapses two or more notices into one row. Re-measured off a fresh capture at the
same 1400x900:

| | before | after |
|---|---|---|
| notices | 347px / 39% | **35px / 4%** |
| the map | 235px / 26% | **59%** |

The visible canvas now shows two nodes with their detail chips, the edge between them, the zoom
controls and the minimap, rather than one node clipped by the fold.

The rule the component encodes is *one notice renders as itself; two or more collapse*. A lone
banner is not a stack, and putting the ordinary single-notice page behind a disclosure to fix the
crowded one would be a bad trade. The collapsed row still names every condition as a badge — it
hides detail, never existence — and takes its own tone from the most severe notice present, so it
cannot make a critical condition look routine.

Deliverable 4's gate is in `visual.spec.ts`: it reads the map container's rendered bounding box
and fails below 50% of viewport height. **59% is the gate's own measurement**, and it supersedes
the 63% I first read off the screenshot by eye — the container's box is the authoritative number
and the pixel estimate was optimistic.

Measured from the box rather than from CSS on purpose: what pushed the map down was three
siblings above it, none of which appears anywhere in the map container's own styles.

The gate is verified in both directions. Raised to 0.9 it fails with `map is 59% of the
viewport`; at 0.5 it passes. Its first version selected `topology-canvas-v2` and **timed out
three times instead of failing**, because that element only exists behind the opt-in v2 renderer
flag — see T-4305. It now selects the renderer-agnostic `topology-map` wrapper and asserts
visibility first, so a wrong selector fails in seconds and names itself.

## Deliverables

1. **Give the map the page.** Options in rough order of preference: collapse the notice banners to
   a single summary row that expands on demand; move them into the existing findings surface
   rather than stacking above the canvas; or pin the canvas to the viewport and let the notices
   scroll over it. Pick one, and measure the resulting share — the acceptance test is a number,
   not an opinion.
2. **Cap the stack.** However the banners are laid out, N simultaneous conditions must not produce
   N stacked banners. Three is already too many and nothing bounds it.
3. **Stop printing wrapped error chains.** The peer-poll path needs an operator-facing message in
   the shape the certificate finding already uses: what is wrong, what it means, what to run. Keep
   the full chain available — a details disclosure, and the log — but not as the default surface.
4. **A gate on the layout share**, so this cannot silently regress: assert from the rendered page
   that the canvas region is at least half the viewport height on a 1400x900 window with the
   fixture's banners present.

## Note

The layout numbers above are read off a real capture rather than computed from CSS, which is the
only way this was findable — and the reason every card in this phase asks for a rendered artifact
rather than only a measurement.

## Deliverable 3 is blocked, deliberately

The raw error text is `SourceStaleness.LastError` (`internal/topology/types.go:282`), and that is
a **documented API field** — `docs/api.md:125` specifies the `staleness` object `GET /topology`
returns. CLAUDE.md is explicit about this class of change:

> API routes, JSON field names, and error format: follow `docs/api.md` exactly — other tasks
> depend on those contracts.

and

> Do not re-litigate decisions … if a decision blocks you, flag it in your final report instead of
> changing it unilaterally.

So I have not changed it. Both available routes alter the contract:

**Option A — change what `lastError` carries.** Put an operator-facing sentence in the existing
field. Smallest diff, no new field, and the SPA needs no change. But it silently redefines a
documented field's meaning for every existing consumer, and it throws away the wrapped chain that
is genuinely useful in a bug report.

**Option B — add a sibling field**, e.g. `lastErrorSummary`, and have the banner show the summary
with the full chain behind a disclosure. Additive, so existing consumers are byte-identical, and
it keeps both audiences served. Costs a new documented field and a `docs/api.md` edit.

**Recommendation: B.** It matches how the codebase already treats this exact tension — the
finding directly below the stale banner reads *"cluster member pve1 has no certificate at
nodes/pve1/pve-ssl.pem — peers cannot verify it and it cannot verify them. To fix: on pve1: pvecm
updatecerts -f"*, which is cause, consequence and command, while the underlying error is still
available elsewhere. The product already knows the shape; only the peer-poll path lacks it.

Note this problem is **older than this card**. `internal/api/collectorrefresh.go`'s T-3603 header
opens with *"The staleness banner used to say 'no successful poll yet — context canceled' and
offer nothing."* That phase's answer was to add a Retry button — the right move at the time, and
it left the message itself untouched. This card is the other half.

What is *not* blocked and could be done independently of the contract decision: the SPA could
truncate the chain to its first clause and put the remainder behind a disclosure, purely
client-side. That is a genuine improvement and needs no API change. It is not done here because it
treats the symptom, and doing it first tends to remove the pressure to do B.
