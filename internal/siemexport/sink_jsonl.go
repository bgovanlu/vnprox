// SPDX-License-Identifier: Apache-2.0

package siemexport

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// jsonlRecord is the exact newline-delimited-JSON wire shape this package
// documents in doc.go's "Field mapping" section — field names match the
// syslog SD-PARAM names one for one, so a SIEM parser written against one
// transport transfers directly to the other.
//
// contract (common, then audit, then finding), not packing — the same
// precedent internal/api's response DTOs and internal/findings.Finding set.
//
//nolint:govet // fieldalignment: wire shape; field order documents the
type jsonlRecord struct {
	Kind      Kind            `json:"kind"`
	At        string          `json:"at"`
	Severity  string          `json:"severity"`
	AuditID   *int64          `json:"auditId,omitempty"`
	Username  string          `json:"username,omitempty"`
	Action    string          `json:"action,omitempty"`
	Target    string          `json:"target,omitempty"`
	Changeset string          `json:"changesetId,omitempty"`
	Result    string          `json:"result,omitempty"`
	IP        string          `json:"ip,omitempty"`
	Detail    json.RawMessage `json:"detail,omitempty"`

	FindingID     string   `json:"findingId,omitempty"`
	Source        string   `json:"source,omitempty"`
	Check         string   `json:"check,omitempty"`
	Transition    string   `json:"transition,omitempty"`
	Nodes         []string `json:"nodes,omitempty"`
	Refs          []string `json:"refs,omitempty"`
	FindingDetail string   `json:"findingDetail,omitempty"`
}

func toJSONLRecord(ev Event) jsonlRecord {
	rec := jsonlRecord{
		Kind:     ev.Kind,
		At:       ev.At.Format("2006-01-02T15:04:05.000000Z07:00"),
		Severity: ev.Severity,
	}
	if ev.Kind == KindAudit {
		id := ev.AuditID
		rec.AuditID = &id
		rec.Username = ev.Username
		rec.Action = ev.Action
		rec.Target = ev.Target
		rec.Changeset = ev.ChangesetID
		rec.Result = ev.Result
		rec.IP = ev.IP
		rec.Detail = ev.Detail
	} else {
		rec.FindingID = ev.FindingID
		rec.Source = ev.Source
		rec.Check = ev.Check
		rec.Transition = ev.Transition
		rec.Nodes = ev.Nodes
		rec.Refs = ev.Refs
		rec.FindingDetail = ev.FindingDetail
	}
	return rec
}

// jsonlSink is a Sink that renders each Event as one JSON object per line.
// Exactly one of file or net is set (config.go's resolveSIEMExportConfig
// enforces "path XOR network+address" before either constructor below is
// reachable).
type jsonlSink struct {
	file *os.File
	net  *netSink
	mu   sync.Mutex
}

// NewJSONLFileSink opens (creating if absent, appending if present) path
// as a JSONL destination.
func NewJSONLFileSink(path string) (Sink, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, fmt.Errorf("siemexport: opening jsonl output %s: %w", path, err)
	}
	return &jsonlSink{file: f}, nil
}

// NewJSONLNetSink streams JSONL over network ("tcp" | "udp" | "unix") to
// address, one line (including its trailing '\n') per Write call — a
// stream-oriented receiver (tcp/unix) sees this as newline-delimited JSON;
// a datagram receiver (udp) sees one JSON object per packet.
func NewJSONLNetSink(network, address string) Sink {
	return &jsonlSink{net: newNetSink(network, address)}
}

func (j *jsonlSink) Send(ctx context.Context, ev Event) error {
	line, err := json.Marshal(toJSONLRecord(ev))
	if err != nil {
		return fmt.Errorf("siemexport: marshaling jsonl event: %w", err)
	}
	line = append(line, '\n')

	if j.file != nil {
		j.mu.Lock()
		defer j.mu.Unlock()
		if _, err := j.file.Write(line); err != nil {
			return fmt.Errorf("siemexport: writing jsonl file: %w", err)
		}
		return nil
	}
	return j.net.write(ctx, line)
}

func (j *jsonlSink) Close() error {
	if j.file != nil {
		j.mu.Lock()
		defer j.mu.Unlock()
		return j.file.Close()
	}
	if j.net != nil {
		return j.net.close()
	}
	return nil
}
