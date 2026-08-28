// SPDX-License-Identifier: Apache-2.0

package flow

import (
	"net"
	"sync"
	"time"
)

// templateKey identifies one cached template: which exporter sent it (its
// UDP source address, since two different exporters may independently reuse
// the same source-id/observation-domain-id + template-id numbering), which
// source/observation domain within that exporter, and which template id.
type templateKey struct {
	exporter string
	domain   uint32
	id       uint16
}

// templateField is one field in a cached template's ordered layout: an
// IANA IPFIX Information Element number (NetFlow v9's own field-type
// numbering is a compatible subset of the same registry) and its declared
// byte length.
type templateField struct {
	typ    uint16
	length uint16
}

type cachedTemplate struct {
	lastSeen time.Time
	fields   []templateField
}

// DefaultTemplateEvictAfter is how long a cached template survives with no
// refresh (a real exporter re-sends its active templates periodically,
// typically every 1-30 minutes) before Prune evicts it — "evicted on
// exporter timeout" per this task's card.
const DefaultTemplateEvictAfter = 30 * time.Minute

// TemplateCache is the per-exporter, per-source-id template store NetFlow
// v9 and IPFIX both need to decode data sets — exactly what a real collector
// must maintain (see this package's doc comment): a template datagram
// populates it (decodeNetFlow9Templates/decodeIPFIXTemplates), a data
// datagram looks a template up by (exporter, domain, id) and decodes
// according to its cached field layout. Safe for concurrent use (one
// listener goroutine per protocol may share a cache across many exporters).
type TemplateCache struct {
	now       func() time.Time
	templates map[templateKey]cachedTemplate
	mu        sync.Mutex
}

// NewTemplateCache builds an empty TemplateCache. now defaults to time.Now.
func NewTemplateCache(now func() time.Time) *TemplateCache {
	if now == nil {
		now = time.Now
	}
	return &TemplateCache{templates: map[templateKey]cachedTemplate{}, now: now}
}

func (c *TemplateCache) set(key templateKey, fields []templateField) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.templates[key] = cachedTemplate{fields: fields, lastSeen: c.now()}
}

func (c *TemplateCache) get(key templateKey) ([]templateField, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.templates[key]
	if !ok {
		return nil, false
	}
	return t.fields, true
}

// Prune evicts every template not refreshed within evictAfter (defaults to
// DefaultTemplateEvictAfter when <= 0) of now, returning the number
// evicted.
func (c *TemplateCache) Prune(evictAfter time.Duration) int {
	if evictAfter <= 0 {
		evictAfter = DefaultTemplateEvictAfter
	}
	cutoff := c.now().Add(-evictAfter)
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for k, t := range c.templates {
		if t.lastSeen.Before(cutoff) {
			delete(c.templates, k)
			n++
		}
	}
	return n
}

// Len reports how many templates are currently cached (test/observability
// helper).
func (c *TemplateCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.templates)
}

// IANA IPFIX Information Element numbers this package understands. NetFlow
// v9 field-type numbers reuse the same registry (Cisco's v9 field dictionary
// and the later IETF IPFIX one share the low-numbered core elements), so one
// applyField switch below serves both decoders. An unrecognized element
// number is decoded (its bytes are consumed so the rest of the record stays
// aligned) but not mapped onto Record — the same forward-tolerant behavior
// any real collector needs for template fields it doesn't understand.
const (
	ieOctetDeltaCount          = 1
	iePacketDeltaCount         = 2
	ieProtocolIdentifier       = 4
	ieSourceTransportPort      = 7
	ieSourceIPv4Address        = 8
	ieIngressInterface         = 10
	ieDestinationTransportPort = 11
	ieDestinationIPv4Address   = 12
	ieEgressInterface          = 14
	ieSourceIPv6Address        = 27
	ieDestinationIPv6Address   = 28
	ieVlanID                   = 58
)

// applyField decodes one already-length-bounded field's raw bytes and
// applies it to rec according to typ (see the ie* constants above).
func applyField(rec *Record, typ uint16, raw []byte) {
	switch typ {
	case ieOctetDeltaCount:
		rec.Bytes = int64(bytesToUint(raw))
	case iePacketDeltaCount:
		rec.Packets = int64(bytesToUint(raw))
	case ieProtocolIdentifier:
		rec.Proto = int(bytesToUint(raw))
	case ieSourceTransportPort:
		rec.SrcPort = int(bytesToUint(raw))
	case ieDestinationTransportPort:
		rec.DstPort = int(bytesToUint(raw))
	case ieSourceIPv4Address:
		if len(raw) == 4 {
			rec.SrcIP = net.IP(raw).String()
		}
	case ieDestinationIPv4Address:
		if len(raw) == 4 {
			rec.DstIP = net.IP(raw).String()
		}
	case ieSourceIPv6Address:
		if len(raw) == 16 {
			rec.SrcIP = net.IP(raw).String()
		}
	case ieDestinationIPv6Address:
		if len(raw) == 16 {
			rec.DstIP = net.IP(raw).String()
		}
	case ieIngressInterface:
		rec.IngressIfIndex = int(bytesToUint(raw))
	case ieEgressInterface:
		rec.EgressIfIndex = int(bytesToUint(raw))
	case ieVlanID:
		rec.VLAN = int(bytesToUint(raw))
	}
}

// bytesToUint interprets raw as a big-endian unsigned integer of whatever
// length the template declared for this field (commonly 1, 2, 4, or 8
// bytes — some exporters use non-power-of-two counter widths).
func bytesToUint(raw []byte) uint64 {
	var v uint64
	for _, b := range raw {
		v = v<<8 | uint64(b)
	}
	return v
}

// decodeDataRecordFields reads exactly the bytes each field in fields
// declares (in order) from r and applies them to rec. Returns false the
// moment r runs out of bytes mid-record — the caller (decodeNetFlow9Data/
// decodeIPFIXData) treats that as "this record, and the rest of the set, is
// truncated" and stops without panicking.
func decodeDataRecordFields(r *breader, fields []templateField, rec *Record) bool {
	for _, f := range fields {
		raw, ok := r.bytes(int(f.length))
		if !ok {
			return false
		}
		applyField(rec, f.typ, raw)
	}
	return true
}
