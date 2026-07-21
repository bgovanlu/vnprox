package procshim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/ingress"
	"github.com/bgovanlu/vnprox/internal/plugin"
	"github.com/bgovanlu/vnprox/internal/switchdrv"
)

// ErrHostClosed is returned by every adapter call once the host's subprocess has
// exited or been torn down — the signal the registry's graceful-degradation path
// turns into a skipped tile/finding pack (T-1702 AC5).
var ErrHostClosed = errors.New("procshim: plugin subprocess is not available")

// Host owns one supervised out-of-process plugin: the subprocess and the single
// framed pipe to it. Calls are serialized (one request/response in flight at a
// time) so the pipe is never interleaved. A broken pipe — the subprocess died,
// was killed, or closed — latches the host closed, so every subsequent call
// fails fast with ErrHostClosed instead of hanging.
type Host struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	mu     sync.Mutex
	broken bool
}

// Start wires cmd's stdin/stdout to the framed pipe and starts the subprocess.
// The caller builds cmd (typically with exec.CommandContext so ctx cancellation
// kills the process) and sets its Env/Args to launch the plugin's serve mode.
func Start(cmd *exec.Cmd) (*Host, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("procshim: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("procshim: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("procshim: starting plugin subprocess: %w", err)
	}
	return &Host{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

// call sends one request and reads one response under the host lock. Any pipe
// error latches the host broken.
func (h *Host) call(method string, params, result any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.broken {
		return ErrHostClosed
	}
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("procshim: marshaling params for %q: %w", method, err)
		}
		raw = b
	}
	if err := writeFrame(h.stdin, request{Method: method, Params: raw}); err != nil {
		h.broken = true
		return fmt.Errorf("procshim call %q: %w", method, joinClosed(err))
	}
	var resp response
	if err := readFrame(h.stdout, &resp); err != nil {
		h.broken = true
		return fmt.Errorf("procshim call %q: %w", method, joinClosed(err))
	}
	if resp.Error != "" {
		return fmt.Errorf("procshim call %q: plugin error: %s", method, resp.Error)
	}
	if result != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("procshim call %q: decoding result: %w", method, err)
		}
	}
	return nil
}

// joinClosed maps a pipe EOF/closed error onto ErrHostClosed so callers (and the
// registry) can uniformly detect a dead subprocess with errors.Is.
func joinClosed(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
		return ErrHostClosed
	}
	return errors.Join(ErrHostClosed, err)
}

// Kill forcibly terminates the subprocess — used by the fault-injection test to
// simulate a plugin dying mid-flight. It latches the host broken.
func (h *Host) Kill() error {
	h.mu.Lock()
	h.broken = true
	h.mu.Unlock()
	if h.cmd.Process == nil {
		return nil
	}
	return h.cmd.Process.Kill()
}

// Close tears the subprocess down cleanly: it closes stdin (the guest's Serve
// loop sees io.EOF and returns), then waits for the process to exit. Safe to
// call more than once. This is the io.Closer the registry invokes on uninstall.
func (h *Host) Close() error {
	h.mu.Lock()
	if h.broken {
		h.mu.Unlock()
		_ = h.cmd.Wait()
		return nil
	}
	h.broken = true
	h.mu.Unlock()
	_ = h.stdin.Close()
	_ = h.cmd.Wait()
	return nil
}

// SwitchDriver returns an adapter implementing plugin.SwitchDriver over the pipe.
func (h *Host) SwitchDriver() plugin.SwitchDriver { return switchDriverClient{h} }

// FlowIngestor returns an adapter implementing plugin.FlowIngestor over the pipe.
func (h *Host) FlowIngestor() plugin.FlowIngestor { return flowIngestorClient{h} }

// FindingProducer returns an adapter implementing plugin.FindingProducer.
func (h *Host) FindingProducer() plugin.FindingProducer { return findingProducerClient{h} }

// IngressDiscoverer returns an adapter implementing plugin.IngressDiscoverer.
func (h *Host) IngressDiscoverer() plugin.IngressDiscoverer { return ingressDiscovererClient{h} }

// DashboardTiles returns an adapter implementing plugin.DashboardTileProvider.
func (h *Host) DashboardTiles() plugin.DashboardTileProvider { return tileProviderClient{h} }

type switchDriverClient struct{ h *Host }

func (c switchDriverClient) PortConfig(_ context.Context, port string) (switchdrv.PortConfig, error) {
	var out switchPortConfigResp
	if err := c.h.call(methodSwitchPortConfig, switchPortReq{Port: port}, &out); err != nil {
		return switchdrv.PortConfig{}, err
	}
	return out.Config, nil
}

func (c switchDriverClient) SetPortConfig(_ context.Context, port string, cfg switchdrv.PortConfig) error {
	return c.h.call(methodSwitchSetPortConfig, switchSetPortConfigReq{Port: port, Config: cfg}, nil)
}

func (c switchDriverClient) PortNeighbor(_ context.Context, port string) (switchdrv.Neighbor, error) {
	var out switchNeighborResp
	if err := c.h.call(methodSwitchPortNeighbor, switchPortReq{Port: port}, &out); err != nil {
		return switchdrv.Neighbor{}, err
	}
	return out.Neighbor, nil
}

func (c switchDriverClient) Close() error {
	return c.h.call(methodSwitchClose, nil, nil)
}

type flowIngestorClient struct{ h *Host }

func (c flowIngestorClient) Ingest(_ context.Context, node, src string, payload []byte) ([]flow.Record, error) {
	var out flowIngestResp
	if err := c.h.call(methodFlowIngest, flowIngestReq{Node: node, Src: src, Payload: payload}, &out); err != nil {
		return nil, err
	}
	return out.Records, nil
}

type findingProducerClient struct{ h *Host }

func (c findingProducerClient) Produce(_ context.Context) ([]findings.Finding, error) {
	var out findingProduceResp
	if err := c.h.call(methodFindingProduce, nil, &out); err != nil {
		return nil, err
	}
	return out.Findings, nil
}

type ingressDiscovererClient struct{ h *Host }

func (c ingressDiscovererClient) Discover(_ context.Context, target ingress.Target) (ingress.ProxyState, error) {
	var out ingressDiscoverResp
	if err := c.h.call(methodIngressDiscover, ingressDiscoverReq{Target: target}, &out); err != nil {
		return ingress.ProxyState{}, err
	}
	return out.State, nil
}

type tileProviderClient struct{ h *Host }

func (c tileProviderClient) Tiles(_ context.Context) ([]plugin.Tile, error) {
	var out tileTilesResp
	if err := c.h.call(methodTileTiles, nil, &out); err != nil {
		return nil, err
	}
	return out.Tiles, nil
}
