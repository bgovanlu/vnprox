package procshim

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/ingress"
	"github.com/bgovanlu/vnprox/internal/plugin"
	"github.com/bgovanlu/vnprox/internal/switchdrv"
)

// Method names — the fixed RPC vocabulary mirrored 1:1 from the plugin extension
// interfaces (wire.proto's service methods). There is deliberately no generic
// "call any method" verb: an unknown method is a hard error on the guest side.
const (
	methodSwitchPortConfig    = "switch.portConfig"
	methodSwitchSetPortConfig = "switch.setPortConfig"
	methodSwitchPortNeighbor  = "switch.portNeighbor"
	methodSwitchClose         = "switch.close"
	methodFlowIngest          = "flow.ingest"
	methodFindingProduce      = "finding.produce"
	methodIngressDiscover     = "ingress.discover"
	methodTileTiles           = "tile.tiles"
)

// maxFrameBytes bounds one wire frame — a malformed or hostile length prefix can
// never make either side allocate an unbounded buffer.
const maxFrameBytes = 16 << 20 // 16 MiB

// request is one framed call: a method name plus its JSON-encoded params.
type request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// response is one framed reply: the JSON-encoded result, or a non-empty error
// string (never both). A transport/dispatch failure is carried in Error.
type response struct {
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// Per-method param/result message shapes (wire.proto messages).

type switchPortReq struct {
	Port string `json:"port"`
}

type switchPortConfigResp struct {
	Config switchdrv.PortConfig `json:"config"`
}

type switchSetPortConfigReq struct {
	Port   string               `json:"port"`
	Config switchdrv.PortConfig `json:"config"`
}

type switchNeighborResp struct {
	Neighbor switchdrv.Neighbor `json:"neighbor"`
}

type flowIngestReq struct {
	Node    string `json:"node"`
	Src     string `json:"src"`
	Payload []byte `json:"payload"`
}

type flowIngestResp struct {
	Records []flow.Record `json:"records"`
}

type findingProduceResp struct {
	Findings []findings.Finding `json:"findings"`
}

type ingressDiscoverReq struct {
	Target ingress.Target `json:"target"`
}

type ingressDiscoverResp struct {
	State ingress.ProxyState `json:"state"`
}

type tileTilesResp struct {
	Tiles []plugin.Tile `json:"tiles"`
}

// writeFrame writes a length-delimited JSON frame for v.
func writeFrame(w io.Writer, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("procshim: marshaling frame: %w", err)
	}
	if len(payload) > maxFrameBytes {
		return fmt.Errorf("procshim: frame too large (%d bytes)", len(payload))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("procshim: writing frame header: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("procshim: writing frame body: %w", err)
	}
	return nil
}

// readFrame reads one length-delimited JSON frame into v. It returns io.EOF
// exactly when the stream is cleanly closed at a frame boundary.
func readFrame(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		if err == io.ErrUnexpectedEOF {
			return io.EOF
		}
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxFrameBytes {
		return fmt.Errorf("procshim: frame length %d exceeds cap", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return fmt.Errorf("procshim: reading frame body: %w", err)
	}
	if err := json.Unmarshal(buf, v); err != nil {
		return fmt.Errorf("procshim: unmarshaling frame: %w", err)
	}
	return nil
}
