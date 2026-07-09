package host

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const sampleCorosyncConf = `
totem {
    version: 2
    cluster_name: testcluster
    transport: knet
    interface {
        linknumber: 0
    }
}

nodelist {
    node {
        name: pve1
        nodeid: 1
        quorum_votes: 1
        ring0_addr: 10.10.0.1
        ring1_addr: 10.10.1.1
    }
    node {
        name: pve2
        nodeid: 2
        quorum_votes: 1
        ring0_addr: 10.10.0.2
    }
}

quorum {
    provider: corosync_votequorum
}

logging {
    debug: off
}
`

func TestParseCorosyncConf(t *testing.T) {
	cfg, err := ParseCorosyncConf([]byte(sampleCorosyncConf))
	if err != nil {
		t.Fatalf("ParseCorosyncConf: %v", err)
	}
	if len(cfg.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2: %+v", len(cfg.Nodes), cfg.Nodes)
	}

	n1, ok := cfg.NodeByName("pve1")
	if !ok {
		t.Fatalf("pve1 not found in %+v", cfg.Nodes)
	}
	if n1.NodeID != 1 {
		t.Errorf("pve1 nodeid = %d, want 1", n1.NodeID)
	}
	if got, want := n1.RingAddrs, []string{"10.10.0.1", "10.10.1.1"}; !stringSlicesEqual(got, want) {
		t.Errorf("pve1 ring addrs = %v, want %v", got, want)
	}

	n2, ok := cfg.NodeByName("pve2")
	if !ok {
		t.Fatalf("pve2 not found in %+v", cfg.Nodes)
	}
	if got, want := n2.RingAddrs, []string{"10.10.0.2"}; !stringSlicesEqual(got, want) {
		t.Errorf("pve2 ring addrs = %v, want %v", got, want)
	}

	if _, ok := cfg.NodeByName("nonexistent"); ok {
		t.Error("NodeByName(nonexistent) = ok, want not found")
	}
}

func TestParseCorosyncConf_IgnoresOtherSections(t *testing.T) {
	cfg, err := ParseCorosyncConf([]byte(`
totem {
    version: 2
    ring0_addr: should-not-be-captured
}
nodelist {
    node {
        name: solo
        ring0_addr: 192.168.1.1
    }
}
`))
	if err != nil {
		t.Fatalf("ParseCorosyncConf: %v", err)
	}
	if len(cfg.Nodes) != 1 {
		t.Fatalf("got %d nodes, want 1 (totem's ring0_addr must not leak in): %+v", len(cfg.Nodes), cfg.Nodes)
	}
	if cfg.Nodes[0].RingAddrs[0] != "192.168.1.1" {
		t.Errorf("ring addr = %q, want 192.168.1.1", cfg.Nodes[0].RingAddrs[0])
	}
}

func TestNodeByName_NilConfig(t *testing.T) {
	var cfg *CorosyncConfig
	if _, ok := cfg.NodeByName("anything"); ok {
		t.Error("NodeByName on nil *CorosyncConfig = ok, want not found")
	}
}

func TestReadCorosyncConf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corosync.conf")
	if err := os.WriteFile(path, []byte(sampleCorosyncConf), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := ReadCorosyncConf(path)
	if err != nil {
		t.Fatalf("ReadCorosyncConf: %v", err)
	}
	if len(cfg.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(cfg.Nodes))
	}
}

func TestReadCorosyncConf_MissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadCorosyncConf(filepath.Join(dir, "does-not-exist.conf"))
	if err == nil {
		t.Fatal("ReadCorosyncConf on a missing file: got nil error, want a wrapped os.PathError")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadCorosyncConf error = %v, want errors.Is(err, os.ErrNotExist)", err)
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
