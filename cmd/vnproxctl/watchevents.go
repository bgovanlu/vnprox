// SPDX-License-Identifier: Apache-2.0

// watchevents.go renders the events `vnproxctl watch` (watchcmd.go)
// receives, in three shapes:
//
//   - a TTY: a colorized, scrolling, append-only log line per event/status
//     transition. Deliberately NOT a full-screen/alternate-buffer TUI —
//     CLAUDE.md's stdlib-first rule plus this card's own "stdlib plus ANSI
//     escapes gets a long way" note make a cursor-repainting curses-style
//     display more machinery than the deliverable calls for, and an
//     append-only stream is what makes Ctrl-C-then-scroll-back useful
//     (a full-screen TUI's history is gone the moment it exits).
//   - a non-TTY stdout (piped/redirected): the identical line shape with
//     every ANSI escape stripped — same fields, same order, so `| grep`/
//     `| awk` sees clean text rather than escape codes.
//   - `-o json`: newline-delimited JSON (documented in docs/cli-json.md's
//     "watch" section) — one object per line, `{"type":"event",...}` for a
//     wire event (every field the server sent, passed through verbatim,
//     which is what keeps this additive-safe against a future event this
//     command doesn't know about yet) or `{"type":"status",...}` for a
//     connection-lifecycle transition, so a script consuming the stream can
//     tell a real gap in coverage from a quiet cluster without watching a
//     second, human-only channel.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// ANSI SGR codes used for TTY rendering. No third-party color library —
// four constants and a reset code is the whole of what's needed.
const (
	ansiReset   = "\x1b[0m"
	ansiCyan    = "\x1b[36m"
	ansiMagenta = "\x1b[35m"
	ansiYellow  = "\x1b[33m"
	ansiBlue    = "\x1b[34m"
	ansiGreen   = "\x1b[32m"
	ansiRed     = "\x1b[31m"
	ansiDim     = "\x1b[2m"
)

// eventColor picks a stable color per known "events"-topic event name
// (docs/architecture.md §13.3's enumerated set); an event name this build
// doesn't recognize (additive-only freeze: a future one is expected) still
// renders, just uncolored — filtering/rendering must never depend on a
// closed list of names.
func eventColor(name string) string {
	switch name {
	case "changeset.status":
		return ansiCyan
	case "drift.changed":
		return ansiMagenta
	case "findings.changed":
		return ansiYellow
	case "audit.appended":
		return ansiBlue
	default:
		return ""
	}
}

// eventRenderer is the single sink every rendered line (event or
// connection-status transition) goes through, so table/JSON/color-vs-plain
// is decided in exactly one place.
//
//nolint:govet // fieldalignment: four short-lived fields on a CLI-local type; ordering for readability, not memory packing.
type eventRenderer struct {
	w       io.Writer
	now     func() time.Time
	color   bool
	jsonOut bool
}

func newEventRenderer(w io.Writer, color, jsonOut bool) *eventRenderer {
	return &eventRenderer{w: w, color: color, jsonOut: jsonOut, now: time.Now}
}

// event renders one decoded wire event (the raw `{"event": "...", ...}`
// object, untouched — decoded by watchLoop in watchcmd.go).
func (r *eventRenderer) event(evt map[string]any) {
	if r.jsonOut {
		out := make(map[string]any, len(evt)+1)
		for k, v := range evt {
			out[k] = v
		}
		out["type"] = "event"
		r.writeJSONLine(out)
		return
	}

	name, _ := evt["event"].(string)
	ts := r.now().Format("15:04:05")
	fields := formatFields(evt, "event")
	line := fmt.Sprintf("%s  %-16s  %s", ts, name, fields)
	if r.color {
		c := eventColor(name)
		if c != "" {
			line = fmt.Sprintf("%s  %s%-16s%s  %s", ts, c, name, ansiReset, fields)
		}
	}
	_, _ = fmt.Fprintln(r.w, line)
}

// connected/reconnected/reconnecting/disconnected/stopped are the
// connection-lifecycle transitions watchLoop reports. Each is a distinct,
// visible line — never a silent state change — because a `watch` that
// stops updating with no indication is worse than one that errors out
// (this card's own framing).

func (r *eventRenderer) connected() {
	r.statusLine("connected", ansiGreen, "connected to the events stream", nil, 0, 0)
}

func (r *eventRenderer) reconnecting(attempt int, err error) {
	r.statusLine("reconnecting", ansiYellow, "connection attempt failed, retrying", err, attempt, 0)
}

func (r *eventRenderer) disconnected(err error) {
	r.statusLine("disconnected", ansiRed, "connection lost, reconnecting", err, 0, 0)
}

func (r *eventRenderer) reconnected(attempt int, gap time.Duration) {
	r.statusLine("reconnected", ansiGreen, "connection restored", nil, attempt, gap)
}

func (r *eventRenderer) stopped() {
	r.statusLine("stopped", ansiDim, "watch stopped", nil, 0, 0)
}

func (r *eventRenderer) statusLine(status, color, summary string, err error, attempt int, gap time.Duration) {
	if r.jsonOut {
		out := map[string]any{
			"type":   "status",
			"status": status,
			"at":     r.now().UTC().Format(time.RFC3339),
		}
		if err != nil {
			out["error"] = err.Error()
		}
		if attempt > 0 {
			out["attempt"] = attempt
		}
		if gap > 0 {
			out["gapSeconds"] = gap.Seconds()
		}
		r.writeJSONLine(out)
		return
	}

	ts := r.now().Format("15:04:05")
	text := summary
	if attempt > 0 {
		text = fmt.Sprintf("%s (attempt %d)", text, attempt)
	}
	if gap > 0 {
		text = fmt.Sprintf("%s, gap %s", text, gap.Round(time.Second))
	}
	if err != nil {
		text = fmt.Sprintf("%s: %v", text, err)
	}
	// "#"-prefixed so a plain-text consumer piping through `grep -v '^#'`
	// can drop connection noise and keep only rendered events, while the
	// default (unfiltered) output still shows the gap — never swallowed.
	line := fmt.Sprintf("%s  # %s", ts, text)
	if r.color && color != "" {
		line = fmt.Sprintf("%s  %s# %s%s", ts, color, text, ansiReset)
	}
	_, _ = fmt.Fprintln(r.w, line)
}

func (r *eventRenderer) writeJSONLine(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return // a marshal failure here would be a bug in this file, not something to surface as a wire event
	}
	_, _ = fmt.Fprintln(r.w, string(b))
}

// formatFields renders every key of evt except skip as a sorted
// `key=value` list — used for the human-readable line shapes (TTY and
// non-TTY alike; only ANSI color differs between them). Sorted so output
// (and tests) are deterministic regardless of Go's randomized map
// iteration order.
func formatFields(evt map[string]any, skip string) string {
	keys := sortedKeys(evt)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if k == skip {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, formatValue(evt[k])))
	}
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " " + p
	}
	return out
}

func formatValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return "-"
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}
