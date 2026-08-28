// SPDX-License-Identifier: Apache-2.0

package plugintest

import (
	"context"
	"io"
	"os"

	"github.com/bgovanlu/vnprox/internal/plugin/procshim"
)

// SampleImpls returns the sample implementations as a procshim.Impls, for the
// guest (subprocess) side of the out-of-process transport.
func SampleImpls() procshim.Impls {
	set := SampleSet()
	return procshim.Impls{
		SwitchDriver:      set.SwitchDriver,
		FlowIngestor:      set.FlowIngestor,
		FindingProducer:   set.FindingProducer,
		IngressDiscoverer: set.IngressDiscoverer,
		DashboardTiles:    set.DashboardTiles,
	}
}

// stdio adapts a separate reader and writer (a process's stdin and stdout) into
// one io.ReadWriteCloser for procshim.Serve.
type stdio struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (s stdio) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s stdio) Write(p []byte) (int, error) { return s.w.Write(p) }
func (s stdio) Close() error {
	_ = s.r.Close()
	return s.w.Close()
}

// ServeStdio runs the sample plugin's guest-side serve loop over os.Stdin/
// os.Stdout until the host closes the pipe. It is what a re-exec'd plugin
// subprocess entrypoint calls (see the conformance test's TestMain): the daemon
// hands the subprocess nothing but this pipe — no DB, no files.
func ServeStdio(ctx context.Context) error {
	return procshim.Serve(ctx, stdio{r: os.Stdin, w: os.Stdout}, SampleImpls())
}
