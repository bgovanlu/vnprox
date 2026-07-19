package k8smock

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/bgovanlu/vnprox/internal/k8s"
)

// Fixture is the hand-readable YAML shape testdata/k8s/*.yaml files use —
// deliberately simpler than real k8s object YAML (no apiVersion/kind/
// resourceVersion boilerplate), since this package's own toK8s converts it
// into the exact wire shapes internal/k8s.Client decodes.
type Fixture struct {
	Nodes      []FixtureNode      `yaml:"nodes"`
	Pods       []FixturePod       `yaml:"pods"`
	Services   []FixtureService   `yaml:"services"`
	DaemonSets []FixtureDaemonSet `yaml:"daemonsets"`
}

// FixtureNode is one testdata node entry.
type FixtureNode struct {
	Annotations map[string]string `yaml:"annotations"`
	Name        string            `yaml:"name"`
	InternalIP  string            `yaml:"internalIP"`
	PodCIDR     string            `yaml:"podCIDR"`
}

// FixturePod is one testdata pod entry.
type FixturePod struct {
	Namespace string `yaml:"namespace"`
	Name      string `yaml:"name"`
	Node      string `yaml:"node"`
	PodIP     string `yaml:"podIP"`
	Phase     string `yaml:"phase"`
}

// FixtureServicePort is one testdata service port entry.
type FixtureServicePort struct {
	Protocol string `yaml:"protocol"`
	Port     int32  `yaml:"port"`
	NodePort int32  `yaml:"nodePort"`
}

// FixtureService is one testdata service entry.
type FixtureService struct {
	Namespace string               `yaml:"namespace"`
	Name      string               `yaml:"name"`
	Type      string               `yaml:"type"`
	ClusterIP string               `yaml:"clusterIP"`
	Ports     []FixtureServicePort `yaml:"ports"`
}

// FixtureDaemonSet is one testdata kube-system daemonset entry.
type FixtureDaemonSet struct {
	Namespace string `yaml:"namespace"`
	Name      string `yaml:"name"`
}

// LoadFixture parses raw fixture YAML bytes.
func LoadFixture(data []byte) (Fixture, error) {
	var f Fixture
	if err := yaml.Unmarshal(data, &f); err != nil {
		return Fixture{}, fmt.Errorf("k8smock: parsing fixture: %w", err)
	}
	return f, nil
}

// LoadFixtureFile reads and parses a fixture YAML file.
func LoadFixtureFile(path string) (Fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, fmt.Errorf("k8smock: reading fixture file %s: %w", path, err)
	}
	return LoadFixture(data)
}

// ToK8s converts f into the real k8s wire-shaped slices internal/k8s.Client
// decodes — the single place this package translates its own readable
// fixture format into the exact JSON shape a real k8s API server would
// return.
func (f Fixture) ToK8s() (nodes []k8s.Node, pods []k8s.Pod, services []k8s.Service, daemonsets []k8s.DaemonSet) {
	for _, n := range f.Nodes {
		node := k8s.Node{
			Metadata: k8s.ObjectMeta{Name: n.Name, Annotations: n.Annotations},
			Spec:     k8s.NodeSpec{PodCIDR: n.PodCIDR},
		}
		if n.InternalIP != "" {
			node.Status.Addresses = []k8s.NodeAddress{{Type: "InternalIP", Address: n.InternalIP}}
		}
		nodes = append(nodes, node)
	}
	for _, p := range f.Pods {
		pods = append(pods, k8s.Pod{
			Metadata: k8s.ObjectMeta{Name: p.Name, Namespace: p.Namespace},
			Spec:     k8s.PodSpec{NodeName: p.Node},
			Status:   k8s.PodStatus{PodIP: p.PodIP, Phase: p.Phase},
		})
	}
	for _, s := range f.Services {
		svc := k8s.Service{
			Metadata: k8s.ObjectMeta{Name: s.Name, Namespace: s.Namespace},
			Spec:     k8s.ServiceSpec{Type: s.Type, ClusterIP: s.ClusterIP},
		}
		for _, p := range s.Ports {
			svc.Spec.Ports = append(svc.Spec.Ports, k8s.ServicePort{
				Port: p.Port, NodePort: p.NodePort, Protocol: p.Protocol,
			})
		}
		services = append(services, svc)
	}
	for _, d := range f.DaemonSets {
		daemonsets = append(daemonsets, k8s.DaemonSet{
			Metadata: k8s.ObjectMeta{Name: d.Name, Namespace: d.Namespace},
		})
	}
	return nodes, pods, services, daemonsets
}
