// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/k8s"
)

func TestParseAndResolve_TokenKubeconfig(t *testing.T) {
	kc, err := k8s.LoadKubeconfigFile("../../testdata/k8s/kubeconfig-token.yaml")
	if err != nil {
		t.Fatalf("LoadKubeconfigFile: %v", err)
	}
	rc, err := k8s.ResolveContext(kc)
	if err != nil {
		t.Fatalf("ResolveContext: %v", err)
	}
	if rc.Server != "https://k8s.example.internal:6443" {
		t.Errorf("Server = %q", rc.Server)
	}
	if rc.Token == "" {
		t.Error("expected a non-empty Token")
	}
	if len(rc.CAData) == 0 || !strings.Contains(string(rc.CAData), "BEGIN CERTIFICATE") {
		t.Errorf("CAData does not look like a decoded PEM certificate: %q", rc.CAData)
	}
	if len(rc.ClientCertData) != 0 || len(rc.ClientKeyData) != 0 {
		t.Error("token kubeconfig should not resolve any client cert material")
	}
}

func TestParseAndResolve_ClientCertKubeconfig(t *testing.T) {
	kc, err := k8s.LoadKubeconfigFile("../../testdata/k8s/kubeconfig-clientcert.yaml")
	if err != nil {
		t.Fatalf("LoadKubeconfigFile: %v", err)
	}
	rc, err := k8s.ResolveContext(kc)
	if err != nil {
		t.Fatalf("ResolveContext: %v", err)
	}
	if rc.Token != "" {
		t.Error("client-cert kubeconfig should not resolve a bearer token")
	}
	if len(rc.ClientCertData) == 0 || len(rc.ClientKeyData) == 0 {
		t.Fatal("expected non-empty client cert/key material")
	}
}

func TestResolveContext_NoCurrentContext(t *testing.T) {
	kc, err := k8s.ParseKubeconfig([]byte(`
apiVersion: v1
kind: Config
clusters: []
contexts: []
users: []
`))
	if err != nil {
		t.Fatalf("ParseKubeconfig: %v", err)
	}
	if _, err := k8s.ResolveContext(kc); !errors.Is(err, k8s.ErrNoCurrentContext) {
		t.Errorf("ResolveContext error = %v, want ErrNoCurrentContext", err)
	}
}

func TestResolveContext_UnknownCluster(t *testing.T) {
	kc, err := k8s.ParseKubeconfig([]byte(`
current-context: ctx1
clusters: []
contexts:
  - name: ctx1
    context:
      cluster: nope
      user: nope
users: []
`))
	if err != nil {
		t.Fatalf("ParseKubeconfig: %v", err)
	}
	if _, err := k8s.ResolveContext(kc); !errors.Is(err, k8s.ErrUnknownCluster) {
		t.Errorf("ResolveContext error = %v, want ErrUnknownCluster", err)
	}
}

func TestResolveContext_NoServer(t *testing.T) {
	kc, err := k8s.ParseKubeconfig([]byte(`
current-context: ctx1
clusters:
  - name: c1
    cluster: {}
contexts:
  - name: ctx1
    context:
      cluster: c1
      user: u1
users:
  - name: u1
    user:
      token: tok
`))
	if err != nil {
		t.Fatalf("ParseKubeconfig: %v", err)
	}
	if _, err := k8s.ResolveContext(kc); !errors.Is(err, k8s.ErrNoServer) {
		t.Errorf("ResolveContext error = %v, want ErrNoServer", err)
	}
}

func TestResolveContext_NoCredential(t *testing.T) {
	kc, err := k8s.ParseKubeconfig([]byte(`
current-context: ctx1
clusters:
  - name: c1
    cluster:
      server: https://example.com
contexts:
  - name: ctx1
    context:
      cluster: c1
      user: u1
users:
  - name: u1
    user: {}
`))
	if err != nil {
		t.Fatalf("ParseKubeconfig: %v", err)
	}
	if _, err := k8s.ResolveContext(kc); !errors.Is(err, k8s.ErrNoCredential) {
		t.Errorf("ResolveContext error = %v, want ErrNoCredential", err)
	}
}
