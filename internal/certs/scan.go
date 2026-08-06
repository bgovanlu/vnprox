package certs

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Default pmxcfs paths. All three are inside /etc/pve, the cluster's
// distributed filesystem, which is why one node can enumerate the whole
// cluster (see the package doc).
const (
	DefaultRoot          = "/etc/pve"
	clusterCAName        = "pve-root-ca.pem"
	nodeLeafName         = "pve-ssl.pem"
	nodeCustomName       = "pveproxy-ssl.pem"
	nodesDir             = "nodes"
	maxCertFileSizeBytes = 1 << 20 // 1 MiB — a certificate chain is kilobytes.
)

// fileKinds is the complete set of per-node filenames this package will ever
// open, and the Kind each yields.
//
// This is an allowlist, not a filter applied after a directory listing. The
// distinction is the whole safety argument: /etc/pve/nodes/<node>/ contains
// pve-ssl.key, and a scanner that listed the directory and skipped keys would
// be one loosened predicate away from reading one. A scanner that only ever
// constructs paths from this map cannot open a file that is not in it,
// whatever else lands in that directory.
var fileKinds = map[string]Kind{
	nodeLeafName:   KindNodeLeaf,
	nodeCustomName: KindCustom,
}

// Options configures a Scan.
type Options struct {
	// Now is the clock, for deterministic tests. Nil means time.Now.
	Now func() time.Time
	// Root is the pmxcfs mount. Empty means DefaultRoot.
	Root string
	// DaemonCertPath is this vnproxd's own configured serving certificate. It
	// is reported as KindDaemon only when it is not already one of the PVE
	// paths above — the default deployment serves /etc/pve/local/pve-ssl.pem,
	// which is a symlink into the node's own directory and would otherwise
	// appear twice under two kinds.
	DaemonCertPath string
	// LocalNode is this daemon's PVE node name, used to attribute the daemon
	// certificate and to resolve /etc/pve/local.
	LocalNode string
}

// FileError is one path that could not be turned into a Certificate.
//
// Reported alongside the successful reads rather than aborting the scan: a
// cluster with one unreadable node directory should still show the other
// nodes' certificates, and "we could not read this one" is itself something
// the operator needs to see rather than have swallowed.
type FileError struct {
	Path  string `json:"path"`
	Node  string `json:"node,omitempty"`
	Error string `json:"error"`
}

// Inventory is the result of a scan.
type Inventory struct {
	ScannedAt time.Time `json:"scannedAt"`
	// ClusterCA is the cluster's root CA, or nil when it could not be read —
	// in which case peer TLS is already failing closed (internal/peer.Trust).
	ClusterCA    *Certificate  `json:"clusterCA,omitempty"`
	Certificates []Certificate `json:"certificates"`
	Errors       []FileError   `json:"errors,omitempty"`
}

// Nodes returns the node names that have at least one certificate, sorted.
func (inv Inventory) Nodes() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range inv.Certificates {
		if c.Node != "" && !seen[c.Node] {
			seen[c.Node] = true
			out = append(out, c.Node)
		}
	}
	sort.Strings(out)
	return out
}

// LeafFor returns the node's pve-ssl.pem certificate, or ok=false.
func (inv Inventory) LeafFor(node string) (Certificate, bool) {
	for _, c := range inv.Certificates {
		if c.Kind == KindNodeLeaf && c.Node == node {
			return c, true
		}
	}
	return Certificate{}, false
}

// Scan builds the cluster-wide inventory. It never returns an error for a
// single unreadable file — those land in Inventory.Errors — and only fails
// outright when the root itself cannot be enumerated.
func Scan(opts Options) (Inventory, error) {
	root := opts.Root
	if root == "" {
		root = DefaultRoot
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	inv := Inventory{ScannedAt: now().UTC()}

	if ca, err := readCert(filepath.Join(root, clusterCAName), KindClusterCA, ""); err != nil {
		inv.Errors = append(inv.Errors, FileError{Path: filepath.Join(root, clusterCAName), Error: err.Error()})
	} else {
		inv.ClusterCA = &ca
		inv.Certificates = append(inv.Certificates, ca)
	}

	nodes, err := listNodes(filepath.Join(root, nodesDir))
	if err != nil {
		// A missing nodes/ directory is not fatal: a dev box with an
		// override cert has no pmxcfs at all, and the daemon certificate
		// below is still worth reporting.
		inv.Errors = append(inv.Errors, FileError{Path: filepath.Join(root, nodesDir), Error: err.Error()})
	}

	for _, node := range nodes {
		for name, kind := range fileKinds {
			path := filepath.Join(root, nodesDir, node, name)
			cert, err := readCert(path, kind, node)
			switch {
			case err == nil:
				inv.Certificates = append(inv.Certificates, cert)
			case errors.Is(err, fs.ErrNotExist):
				// pveproxy-ssl.pem is optional and absent on most nodes;
				// a missing pve-ssl.pem is a real condition, but it is the
				// cert_missing check's job to say so against the cluster's
				// membership list, not this loop's — a node directory is not
				// the authority on who is in the cluster.
				continue
			default:
				inv.Errors = append(inv.Errors, FileError{Path: path, Node: node, Error: err.Error()})
			}
		}
	}

	inv.addDaemonCert(opts, root)
	sortCertificates(inv.Certificates)
	return inv, nil
}

// addDaemonCert reports this daemon's own serving certificate when it is not
// already covered by the PVE paths scanned above.
func (inv *Inventory) addDaemonCert(opts Options, root string) {
	if opts.DaemonCertPath == "" {
		return
	}
	resolved := resolvePath(opts.DaemonCertPath)
	for _, c := range inv.Certificates {
		if resolvePath(c.Path) == resolved {
			return
		}
	}
	cert, err := readCert(opts.DaemonCertPath, KindDaemon, opts.LocalNode)
	if err != nil {
		inv.Errors = append(inv.Errors, FileError{Path: opts.DaemonCertPath, Node: opts.LocalNode, Error: err.Error()})
		return
	}
	_ = root
	inv.Certificates = append(inv.Certificates, cert)
}

// resolvePath follows symlinks where possible so /etc/pve/local/pve-ssl.pem
// and /etc/pve/nodes/<local>/pve-ssl.pem are recognised as the same file.
// Falls back to the literal path when the link cannot be resolved, which is
// the safe direction: the worst case is reporting one certificate twice, not
// missing one.
func resolvePath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func listNodes(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("certs: listing %s: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// readCert reads and parses one certificate file.
//
// The size cap is a guard against a path that turns out to be something
// enormous, not a security boundary — but it does mean a mistake in the
// allowlist above could never stream an unbounded file into memory.
func readCert(path string, kind Kind, node string) (Certificate, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Certificate{}, err
	}
	if info.Size() > maxCertFileSizeBytes {
		return Certificate{}, fmt.Errorf("certs: %s is %d bytes, larger than the %d-byte cap for a certificate file", path, info.Size(), maxCertFileSizeBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Certificate{}, err
	}
	return parse(raw, kind, node, path)
}

// sortCertificates orders the inventory for display: the cluster CA first,
// then by node, then by kind. Stable output matters because this feeds an API
// response and a table; an order that varied per scan would make every poll
// look like a change.
func sortCertificates(certs []Certificate) {
	kindRank := map[Kind]int{KindClusterCA: 0, KindNodeLeaf: 1, KindCustom: 2, KindDaemon: 3}
	sort.SliceStable(certs, func(i, j int) bool {
		a, b := certs[i], certs[j]
		if a.Node != b.Node {
			// The CA has no node and sorts first.
			if a.Node == "" {
				return true
			}
			if b.Node == "" {
				return false
			}
			return a.Node < b.Node
		}
		if kindRank[a.Kind] != kindRank[b.Kind] {
			return kindRank[a.Kind] < kindRank[b.Kind]
		}
		return a.Path < b.Path
	})
}

// VerifyChain reports whether leaf verifies against the cluster CA held in
// caPEM-equivalent form.
//
// It re-reads the CA from disk rather than reconstructing it from the parsed
// Certificate, because Certificate deliberately carries no DER — see the type's
// doc comment. The cost is one small file read per verification pass.
func VerifyChain(root string, leafPath string, now time.Time) error {
	if root == "" {
		root = DefaultRoot
	}
	caRaw, err := os.ReadFile(filepath.Join(root, clusterCAName))
	if err != nil {
		return fmt.Errorf("certs: reading cluster CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caRaw) {
		return fmt.Errorf("certs: cluster CA at %s: %w", filepath.Join(root, clusterCAName), ErrNoCertificate)
	}
	leafRaw, err := os.ReadFile(leafPath)
	if err != nil {
		return fmt.Errorf("certs: reading %s: %w", leafPath, err)
	}
	leaf, err := firstCertificate(leafRaw)
	if err != nil {
		return err
	}
	// KeyUsages is explicitly any: pve-ssl.pem is a server certificate, but a
	// custom/ACME certificate may legitimately carry a different EKU set, and
	// this function's question is "was this issued by our CA", not "is it
	// usable for TLS server auth" (which the handshake itself decides).
	_, err = leaf.Verify(x509.VerifyOptions{
		Roots:       pool,
		CurrentTime: now,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil {
		return fmt.Errorf("certs: verifying %s against the cluster CA: %w", leafPath, err)
	}
	return nil
}

func firstCertificate(pemBytes []byte) (*x509.Certificate, error) {
	rest := pemBytes
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, ErrNoCertificate
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		return x509.ParseCertificate(block.Bytes)
	}
}

// LocalNodeFromRoot resolves /etc/pve/local to the node name it points at.
// Returns "" when the link is absent (a dev box, or a root fixture without
// one), which every caller treats as "unknown", never as a node named "".
func LocalNodeFromRoot(root string) string {
	if root == "" {
		root = DefaultRoot
	}
	target, err := os.Readlink(filepath.Join(root, "local"))
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(filepath.Clean(target), nodesDir+string(filepath.Separator))
}
