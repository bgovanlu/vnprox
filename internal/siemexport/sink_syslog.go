// SPDX-License-Identifier: Apache-2.0

package siemexport

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// syslogSeverity maps Event.Severity to RFC 5424 §6.2.1's numeric Severity
// (0 Emergency .. 7 Debug). Only three of the eight levels are reachable
// from this package's own closed Severity vocabulary (event.go); anything
// else (there is no other value NewAuditEvent/NewFindingEvent can produce)
// falls back to Informational.
func syslogSeverity(sev string) int {
	switch sev {
	case SeverityError:
		return 3 // Error
	case SeverityWarning:
		return 4 // Warning
	default:
		return 6 // Informational
	}
}

// sdElementID names T-4012's own RFC 5424 §7.2.2 example enterprise
// number (32473 is the number the RFC itself reserves for
// documentation/example SD-IDs — vnprox has no IANA-registered PEN of its
// own, so every field beyond the RFC's fixed header lives inside an
// SD-ELEMENT keyed off that reserved number, exactly as the RFC's own
// examples do).
func sdElementID(k Kind) string {
	if k == KindFinding {
		return "vnproxFinding@32473"
	}
	return "vnproxAudit@32473"
}

// syslogSink is a Sink that renders each Event as one RFC 5424 message and
// writes it to network/address (netSink). TCP/unix connections use RFC
// 6587 §3.4.1 octet-counting framing ("MSGLEN SP SYSLOG-MSG") — the
// unambiguous framing most SIEM TCP syslog listeners expect, and the
// reason this package does not offer bare newline-delimited syslog:
// MSG itself may legitimately contain a newline, which a
// newline-delimited framing cannot tell apart from a frame boundary. UDP
// carries one message per datagram, which needs no framing at all.
//
// allocation path.
//
//nolint:govet // fieldalignment: one syslogSink per Sink, never a hot
type syslogSink struct {
	hostname string
	appName  string
	pid      string
	net      *netSink
	facility int
	framed   bool // true for tcp/unix (octet-counting), false for udp
}

// NewSyslogSink constructs a syslog Sink over network ("tcp" | "udp" |
// "unix") and address (host:port for tcp/udp, socket path for unix).
// facility is the RFC 5424 facility number (0-23) — config.go's
// resolveSIEMExportConfig validates that range before this is ever
// called, so it is not re-validated here.
func NewSyslogSink(network, address string, facility int) Sink {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "-"
	}
	return &syslogSink{
		net:      newNetSink(network, address),
		framed:   network != "udp",
		facility: facility,
		hostname: hostname,
		appName:  "vnproxd",
		pid:      strconv.Itoa(os.Getpid()),
	}
}

func (s *syslogSink) Send(ctx context.Context, ev Event) error {
	msg := formatRFC5424(ev, s.facility, s.hostname, s.appName, s.pid)
	frame := []byte(msg)
	if s.framed {
		// RFC 6587 octet-counting: the message length (in octets), a
		// single space, then the message itself — no trailing delimiter
		// needed or added.
		frame = []byte(fmt.Sprintf("%d %s", len(frame), msg))
	}
	return s.net.write(ctx, frame)
}

func (s *syslogSink) Close() error { return s.net.close() }

// formatRFC5424 renders one RFC 5424 syslog message. See doc.go's "Field
// mapping" section for the documented SD-PARAM contract this produces.
func formatRFC5424(ev Event, facility int, hostname, appName, pid string) string {
	pri := facility*8 + syslogSeverity(ev.Severity)
	timestamp := ev.At.Format("2006-01-02T15:04:05.000000Z07:00")
	msgID := "audit"
	if ev.Kind == KindFinding {
		msgID = "finding"
	}

	var sd strings.Builder
	sd.WriteByte('[')
	sd.WriteString(sdElementID(ev.Kind))
	writeSDParam(&sd, "severity", ev.Severity)
	if ev.Kind == KindAudit {
		writeSDParam(&sd, "id", strconv.FormatInt(ev.AuditID, 10))
		writeSDParam(&sd, "username", ev.Username)
		writeSDParam(&sd, "action", ev.Action)
		writeSDParam(&sd, "target", ev.Target)
		writeSDParam(&sd, "changesetId", ev.ChangesetID)
		writeSDParam(&sd, "result", ev.Result)
		writeSDParam(&sd, "ip", ev.IP)
		if len(ev.Detail) > 0 {
			writeSDParam(&sd, "detail", string(ev.Detail))
		}
	} else {
		writeSDParam(&sd, "findingId", ev.FindingID)
		writeSDParam(&sd, "source", ev.Source)
		writeSDParam(&sd, "check", ev.Check)
		writeSDParam(&sd, "transition", ev.Transition)
		if len(ev.Nodes) > 0 {
			writeSDParam(&sd, "nodes", strings.Join(ev.Nodes, ","))
		}
		if len(ev.Refs) > 0 {
			writeSDParam(&sd, "refs", strings.Join(ev.Refs, ","))
		}
		if ev.FindingDetail != "" {
			writeSDParam(&sd, "detail", ev.FindingDetail)
		}
	}
	sd.WriteByte(']')

	return fmt.Sprintf("<%d>1 %s %s %s %s %s %s %s",
		pri, timestamp, sdName(hostname), appName, pid, sdName(msgID), sd.String(), formatMSG(ev))
}

// sdName returns "-" (RFC 5424's NILVALUE) for an empty field — HOSTNAME
// and MSGID must never be an empty string on the wire.
func sdName(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// writeSDParam appends ` name="escaped(value)"` to sd, skipping entirely
// when value is empty — an absent SD-PARAM rather than an empty one, so a
// SIEM parser extracting fields sees "not present" instead of "present but
// blank" for, say, a pre-T-2902 audit row with no recorded IP.
func writeSDParam(sd *strings.Builder, name, value string) {
	if value == "" {
		return
	}
	sd.WriteByte(' ')
	sd.WriteString(name)
	sd.WriteString(`="`)
	sd.WriteString(escapeSDValue(value))
	sd.WriteByte('"')
}

// escapeSDValue backslash-escapes the three PARAM-VALUE characters RFC
// 5424 §6.3.3 requires escaped: `"`, `\`, and `]`.
func escapeSDValue(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '"', '\\', ']':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// formatMSG renders the human-readable free-text MSG part — for a SIEM
// parser that reads MSG instead of (or in addition to) the SD-PARAMs
// above.
func formatMSG(ev Event) string {
	if ev.Kind == KindAudit {
		return fmt.Sprintf("audit: user=%s action=%s target=%s result=%s", sdName(ev.Username), sdName(ev.Action), sdName(ev.Target), sdName(ev.Result))
	}
	return fmt.Sprintf("finding: %s %s (%s) severity=%s refs=%s", ev.Transition, sdName(ev.Check), sdName(ev.Source), ev.Severity, sdName(strings.Join(ev.Refs, ",")))
}
