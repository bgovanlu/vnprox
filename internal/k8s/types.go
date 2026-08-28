// SPDX-License-Identifier: Apache-2.0

package k8s

// The types below are a deliberately minimal subset of the real Kubernetes
// API's JSON wire shapes — only the fields this package's CNI detection,
// node/guest correlation, overlay model, and NodePort exposure check
// actually read. They are hand-decoded (encoding/json, no client-go/
// apimachinery types) per this package's doc comment.

// ObjectMeta is the common metadata block every k8s object embeds.
type ObjectMeta struct {
	Annotations map[string]string `json:"annotations,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace,omitempty"`
}

// --- Node (GET /api/v1/nodes) ---------------------------------------------

// Node is one cluster node, as returned by GET /api/v1/nodes.
type Node struct {
	Metadata ObjectMeta `json:"metadata"`
	Spec     NodeSpec   `json:"spec"`
	Status   NodeStatus `json:"status"`
}

// NodeSpec carries the node's advertised pod CIDR(s) — real, per-node
// values a k8s API server always populates once a node has joined
// (assuming the cluster's CNI uses node-scoped pod CIDR allocation, which
// Flannel/Calico's default IPAM and Cilium's cluster-scope mode all do;
// see overlay.go's doc comment for the documented gap when a CNI manages
// pod addressing entirely outside NodeSpec).
type NodeSpec struct {
	PodCIDR  string   `json:"podCIDR,omitempty"`
	PodCIDRs []string `json:"podCIDRs,omitempty"`
}

// NodeStatus carries the node's reported addresses.
type NodeStatus struct {
	Addresses []NodeAddress `json:"addresses,omitempty"`
}

// NodeAddress is one reported address; Type is "InternalIP"|"ExternalIP"|
// "Hostname"|... — this package only ever reads "InternalIP" (node<->guest
// correlation matches against the same address PVE's own IPAM/guest-agent
// data would report for a cluster-internal NIC).
type NodeAddress struct {
	Type    string `json:"type"`
	Address string `json:"address"`
}

// NodeList is GET /api/v1/nodes' envelope.
type NodeList struct {
	Items []Node `json:"items"`
}

// InternalIP returns n's first status.addresses entry of type
// "InternalIP", or "" if none is reported.
func (n Node) InternalIP() string {
	for _, a := range n.Status.Addresses {
		if a.Type == "InternalIP" {
			return a.Address
		}
	}
	return ""
}

// --- Pod (GET /api/v1/pods) -----------------------------------------------

// Pod is one pod, as returned by GET /api/v1/pods (cluster-wide — no
// namespace segment, matching the card's named endpoint).
type Pod struct {
	Metadata ObjectMeta `json:"metadata"`
	Spec     PodSpec    `json:"spec"`
	Status   PodStatus  `json:"status"`
}

// PodSpec carries the pod's assigned node.
type PodSpec struct {
	NodeName string `json:"nodeName,omitempty"`
}

// PodStatus carries the pod's assigned IP and lifecycle phase.
type PodStatus struct {
	PodIP string `json:"podIP,omitempty"`
	Phase string `json:"phase,omitempty"`
}

// PodList is GET /api/v1/pods' envelope.
type PodList struct {
	Items []Pod `json:"items"`
}

// --- Service (GET /api/v1/services) ---------------------------------------

// Service is one service, as returned by GET /api/v1/services.
type Service struct {
	Metadata ObjectMeta  `json:"metadata"`
	Spec     ServiceSpec `json:"spec"`
}

// ServiceSpec carries the service's type, ClusterIP, and port list.
type ServiceSpec struct {
	// Type is "ClusterIP" (the default when omitted)|"NodePort"|
	// "LoadBalancer"|"ExternalName".
	Type      string        `json:"type,omitempty"`
	ClusterIP string        `json:"clusterIP,omitempty"`
	Ports     []ServicePort `json:"ports,omitempty"`
}

// EffectiveType returns Spec.Type, defaulting to "ClusterIP" per real k8s
// API defaulting semantics (an object read fresh from a live apiserver
// always carries an explicit type, but a hand-built test fixture might
// reasonably omit it).
func (s ServiceSpec) EffectiveType() string {
	if s.Type == "" {
		return "ClusterIP"
	}
	return s.Type
}

// ServicePort is one exposed port. NodePort is only meaningful (nonzero)
// on a Type: NodePort|LoadBalancer service.
type ServicePort struct {
	Name     string `json:"name,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Port     int32  `json:"port"`
	NodePort int32  `json:"nodePort,omitempty"`
}

// EffectiveProtocol returns Protocol, defaulting to "TCP" per real k8s API
// defaulting semantics (same reasoning as ServiceSpec.EffectiveType).
func (p ServicePort) EffectiveProtocol() string {
	if p.Protocol == "" {
		return "TCP"
	}
	return p.Protocol
}

// ServiceList is GET /api/v1/services' envelope.
type ServiceList struct {
	Items []Service `json:"items"`
}

// --- DaemonSet (GET /apis/apps/v1/namespaces/kube-system/daemonsets) ------

// DaemonSet is one kube-system DaemonSet — this package only ever reads
// its name (CNI detection, cni.go), never its pod template/spec.
type DaemonSet struct {
	Metadata ObjectMeta `json:"metadata"`
}

// DaemonSetList is the daemonsets envelope.
type DaemonSetList struct {
	Items []DaemonSet `json:"items"`
}
