// kubeconfig.go implements a minimal kubeconfig (v1, `apiVersion:
// v1`/`kind: Config`) parser: just enough of the real format — clusters/
// users/contexts/current-context, server URL, CA data, client
// cert/key or bearer token, insecure-skip-tls-verify — to build an
// *http.Client that authenticates read-only GET requests, per this
// package's doc comment. Anything else a real kubeconfig can carry
// (exec-based credential plugins, auth-provider plugins, multiple
// contexts selected at call time) is out of scope: ResolveContext always
// resolves current-context only, and returns ErrNoCredential rather than
// guessing at an unsupported credential form.
//
// Reuses gopkg.in/yaml.v3 (already an approved dependency, T-1101) — not a
// second YAML library.

package k8s

import (
	"encoding/base64"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Kubeconfig is the parsed shape of a kubeconfig file's top-level fields
// this package understands.
type Kubeconfig struct {
	CurrentContext string         `yaml:"current-context"`
	Clusters       []NamedCluster `yaml:"clusters"`
	Users          []NamedUser    `yaml:"users"`
	Contexts       []NamedContext `yaml:"contexts"`
}

// NamedCluster is one `clusters[]` entry.
type NamedCluster struct {
	Name    string      `yaml:"name"`
	Cluster ClusterInfo `yaml:"cluster"`
}

// ClusterInfo is a cluster entry's own fields.
type ClusterInfo struct {
	Server                   string `yaml:"server"`
	CertificateAuthorityData string `yaml:"certificate-authority-data"`
	CertificateAuthority     string `yaml:"certificate-authority"`
	InsecureSkipTLSVerify    bool   `yaml:"insecure-skip-tls-verify"`
}

// NamedUser is one `users[]` entry.
type NamedUser struct {
	Name string   `yaml:"name"`
	User UserInfo `yaml:"user"`
}

// UserInfo is a user entry's own fields — credential material only, never
// a write-scoped field (this package has no such concept: whatever a real
// kubeconfig's user stanza can also carry — e.g. impersonation config — is
// simply not modeled here, since nothing in this package would ever use
// it).
type UserInfo struct {
	Token                 string `yaml:"token"`
	ClientCertificateData string `yaml:"client-certificate-data"`
	ClientKeyData         string `yaml:"client-key-data"`
}

// NamedContext is one `contexts[]` entry.
type NamedContext struct {
	Name    string      `yaml:"name"`
	Context ContextInfo `yaml:"context"`
}

// ContextInfo is a context entry's own fields.
type ContextInfo struct {
	Cluster string `yaml:"cluster"`
	User    string `yaml:"user"`
}

// ParseKubeconfig parses raw kubeconfig YAML bytes.
func ParseKubeconfig(data []byte) (Kubeconfig, error) {
	var kc Kubeconfig
	if err := yaml.Unmarshal(data, &kc); err != nil {
		return Kubeconfig{}, fmt.Errorf("k8s: parsing kubeconfig: %w", err)
	}
	return kc, nil
}

// ResolvedConfig is what ResolveContext distills a Kubeconfig down to: just
// enough to build an authenticated, read-only *http.Client against exactly
// one cluster (client.go's NewClient consumes this directly).
type ResolvedConfig struct {
	Server                string
	Token                 string
	CAData                []byte
	ClientCertData        []byte
	ClientKeyData         []byte
	InsecureSkipTLSVerify bool
}

// ResolveContext resolves kc's current-context into a ResolvedConfig,
// reading only the base64-inlined *-data fields (never a *-file-path
// field — see ResolvedConfig's doc comment). Returns ErrNoCurrentContext/
// ErrUnknownCluster/ErrUnknownUser/ErrNoServer/ErrNoCredential rather than
// guessing at a partially-specified or unsupported kubeconfig.
func ResolveContext(kc Kubeconfig) (ResolvedConfig, error) {
	if kc.CurrentContext == "" {
		return ResolvedConfig{}, ErrNoCurrentContext
	}
	var ctx ContextInfo
	found := false
	for _, c := range kc.Contexts {
		if c.Name == kc.CurrentContext {
			ctx = c.Context
			found = true
			break
		}
	}
	if !found {
		return ResolvedConfig{}, ErrNoCurrentContext
	}

	var cluster ClusterInfo
	found = false
	for _, c := range kc.Clusters {
		if c.Name == ctx.Cluster {
			cluster = c.Cluster
			found = true
			break
		}
	}
	if !found {
		return ResolvedConfig{}, ErrUnknownCluster
	}
	if cluster.Server == "" {
		return ResolvedConfig{}, ErrNoServer
	}

	var user UserInfo
	found = false
	for _, u := range kc.Users {
		if u.Name == ctx.User {
			user = u.User
			found = true
			break
		}
	}
	if !found {
		return ResolvedConfig{}, ErrUnknownUser
	}

	rc := ResolvedConfig{
		Server:                cluster.Server,
		InsecureSkipTLSVerify: cluster.InsecureSkipTLSVerify,
	}
	if cluster.CertificateAuthorityData != "" {
		ca, err := base64.StdEncoding.DecodeString(cluster.CertificateAuthorityData)
		if err != nil {
			return ResolvedConfig{}, fmt.Errorf("k8s: decoding certificate-authority-data: %w", err)
		}
		rc.CAData = ca
	}

	switch {
	case user.Token != "":
		rc.Token = user.Token
	case user.ClientCertificateData != "" && user.ClientKeyData != "":
		cert, err := base64.StdEncoding.DecodeString(user.ClientCertificateData)
		if err != nil {
			return ResolvedConfig{}, fmt.Errorf("k8s: decoding client-certificate-data: %w", err)
		}
		key, err := base64.StdEncoding.DecodeString(user.ClientKeyData)
		if err != nil {
			return ResolvedConfig{}, fmt.Errorf("k8s: decoding client-key-data: %w", err)
		}
		rc.ClientCertData = cert
		rc.ClientKeyData = key
	default:
		return ResolvedConfig{}, ErrNoCredential
	}

	return rc, nil
}

// LoadKubeconfigFile reads and parses a kubeconfig from path — a thin
// convenience wrapper (ParseKubeconfig does the real work) used by
// k8smock/tests and any future CLI-side loader; internal/api's routes
// never read from a filesystem path — a kubeconfig arrives as a request
// body and is stored encrypted, never written to disk unencrypted.
func LoadKubeconfigFile(path string) (Kubeconfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Kubeconfig{}, fmt.Errorf("k8s: reading kubeconfig file %s: %w", path, err)
	}
	return ParseKubeconfig(data)
}
