// overlay.go implements Overlay: the pod/service CIDR model, node<->guest
// correlation, and detected CNI GET /k8s/{clusterId}/overlay serves — the
// data T-1502's map layer renders and K8sResolver (resolver.go) indexes
// for flow attribution. See doc.go's "Pod/service network model" section
// for why this never claims a guessed "service CIDR".
//
// Documented gap: PodCIDRs is only ever populated from
// Node.spec.podCIDR/podCIDRs — real for any CNI using node-scoped pod
// address allocation (Flannel and Calico's default IPAM both do; Cilium's
// default cluster-scope IPAM also annotates NodeSpec). A CNI configured
// for fully node-independent pod addressing would leave this empty for
// affected nodes rather than this package guessing a block — Overlay
// simply carries no PodCIDR entry for that node, same "never guessed"
// treatment as everything else in this package.

package k8s

import "net"

// PodCIDR is one node's advertised pod-network block.
type PodCIDR struct {
	Node string `json:"node"`
	CIDR string `json:"cidr"`
}

// PodSummary is one pod, carried for T-1502's pod-drilldown selection
// (pod -> node-guest -> bridge -> bond) — deliberately minimal (no
// container/spec detail this package has no read path for beyond the four
// named endpoints).
type PodSummary struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Node      string `json:"node,omitempty"`
	PodIP     string `json:"podIp,omitempty"`
	Phase     string `json:"phase,omitempty"`
}

// ServicePortInfo is one exposed service port, in Overlay's own JSON shape
// (types.go's ServicePort mirrored 1:1 rather than reused directly, so
// this file's exported shape doesn't leak the k8s wire JSON tags).
type ServicePortInfo struct {
	Name     string `json:"name,omitempty"`
	Protocol string `json:"protocol"`
	Port     int32  `json:"port"`
	NodePort int32  `json:"nodePort,omitempty"`
}

// ServiceInfo is one service, carried in full (never a service CIDR
// guess — see this file's package-level doc comment).
type ServiceInfo struct {
	Namespace string            `json:"namespace"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	ClusterIP string            `json:"clusterIp,omitempty"`
	Ports     []ServicePortInfo `json:"ports,omitempty"`
}

// Overlay is GET /k8s/{clusterId}/overlay's response model.
type Overlay struct {
	ClusterID   string            `json:"clusterId"`
	CNI         CNI               `json:"cni"`
	PodCIDRs    []PodCIDR         `json:"podCidrs"`
	Services    []ServiceInfo     `json:"services"`
	Pods        []PodSummary      `json:"pods"`
	Nodes       []NodeCorrelation `json:"nodes"`
	GeneratedAt int64             `json:"generatedAt"`
}

// BuildOverlay assembles Overlay from one poll's raw API results plus a
// guest-correlation index (nil-safe, see CorrelateNodes). Pure function of
// its inputs — no I/O, no clock read (GeneratedAt is the caller's
// responsibility to stamp, mirroring every other *Response's
// `generatedAt` field elsewhere in this codebase, e.g.
// ingressStatusResponse).
func BuildOverlay(clusterID string, nodes []Node, pods []Pod, services []Service, daemonsets []DaemonSet, index GuestIPIndex) Overlay {
	ov := Overlay{
		ClusterID: clusterID,
		CNI:       DetectCNI(nodes, daemonsets),
		Nodes:     CorrelateNodes(nodes, index),
	}

	for _, n := range nodes {
		cidrs := n.Spec.PodCIDRs
		if len(cidrs) == 0 && n.Spec.PodCIDR != "" {
			cidrs = []string{n.Spec.PodCIDR}
		}
		for _, cidr := range cidrs {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				continue // never surface a malformed value as if it were a real CIDR
			}
			ov.PodCIDRs = append(ov.PodCIDRs, PodCIDR{Node: n.Metadata.Name, CIDR: cidr})
		}
	}

	for _, s := range services {
		si := ServiceInfo{
			Namespace: s.Metadata.Namespace,
			Name:      s.Metadata.Name,
			Type:      s.Spec.EffectiveType(),
			ClusterIP: s.Spec.ClusterIP,
		}
		for _, p := range s.Spec.Ports {
			si.Ports = append(si.Ports, ServicePortInfo{
				Name: p.Name, Port: p.Port, NodePort: p.NodePort, Protocol: p.EffectiveProtocol(),
			})
		}
		ov.Services = append(ov.Services, si)
	}

	for _, p := range pods {
		ov.Pods = append(ov.Pods, PodSummary{
			Namespace: p.Metadata.Namespace, Name: p.Metadata.Name,
			Node: p.Spec.NodeName, PodIP: p.Status.PodIP, Phase: p.Status.Phase,
		})
	}

	return ov
}
