// cni.go implements T-1501's best-effort CNI detector: Flannel, Calico,
// and Cilium are recognized from markers a default install of each
// actually leaves behind; anything else — a different CNI, a
// non-default install, or no recognizable marker at all — reports
// CNIUnknown rather than guessing (the card's own words: "a fourth/
// unknown CNI is reported as unknown rather than guessed").

package k8s

// CNI names a detected (or undetected) Container Network Interface plugin.
type CNI string

const (
	CNIFlannel CNI = "flannel"
	CNICalico  CNI = "calico"
	CNICilium  CNI = "cilium"
	CNIUnknown CNI = "unknown"
)

// flannelBackendAnnotation is the annotation Flannel's kube-subnet-manager
// writes onto every Node object it manages, naming its backend (typically
// "vxlan", also "host-gw"/"udp"/... — any non-empty value from Flannel's
// own annotation key is treated as "this is Flannel", not just "vxlan"
// specifically, since the annotation's mere presence is already
// Flannel-specific; docs/data-model.md's card names the VXLAN backend as
// the concrete example this detector is built against).
const flannelBackendAnnotation = "flannel.alpha.coreos.com/backend-type"

// calicoDaemonSetName / ciliumDaemonSetName are the well-known DaemonSet
// names each CNI's default kube-system install creates.
const (
	calicoDaemonSetName = "calico-node"
	ciliumDaemonSetName = "cilium"
)

// DetectCNI inspects nodes' annotations and kube-system daemonsets for
// each CNI's own default-install marker. DaemonSet markers are checked
// first (a DaemonSet name is a stronger, install-time-declared signal than
// an annotation a node-agent writes after the fact); Flannel's node
// annotation is checked second. No marker of any kind recognized ->
// CNIUnknown.
func DetectCNI(nodes []Node, daemonsets []DaemonSet) CNI {
	for _, ds := range daemonsets {
		switch ds.Metadata.Name {
		case calicoDaemonSetName:
			return CNICalico
		case ciliumDaemonSetName:
			return CNICilium
		}
	}
	for _, n := range nodes {
		if v, ok := n.Metadata.Annotations[flannelBackendAnnotation]; ok && v != "" {
			return CNIFlannel
		}
	}
	return CNIUnknown
}
