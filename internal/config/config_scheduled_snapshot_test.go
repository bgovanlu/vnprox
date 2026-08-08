package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// loadFragment loads a config consisting of the minimum a dev environment
// needs (an override TLS pair, since these tests do not run on a PVE node)
// plus the caller's own [retention] fragment.
func loadFragment(t *testing.T, body string) (*Config, error) {
	t.Helper()
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := "[server]\ntls_cert = \"" + certPath + "\"\ntls_key = \"" + keyPath + "\"\n\n" + body
	return Load(writeTemp(t, "vnprox.toml", toml), discardLogger())
}

// T-2401 AC6, and the design decision the card is explicit about: the feature
// is OFF unless the operator asks for it, because a capture is a full read of
// every node's config file.
func TestRetention_SnapshotScheduleIsOffByDefault(t *testing.T) {
	cfg, err := loadFragment(t, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Retention.SnapshotScheduleInterval != 0 {
		t.Fatalf("snapshot_schedule_interval defaulted to %s; scheduled snapshots must be opt-in",
			cfg.Retention.SnapshotScheduleInterval)
	}
	// The retention ceiling still has a default, so an operator who enables
	// the interval does not also have to remember to bound the count.
	if cfg.Retention.SnapshotScheduleKeep != DefaultSnapshotScheduleKeep {
		t.Fatalf("snapshot_schedule_keep = %d, want the default %d",
			cfg.Retention.SnapshotScheduleKeep, DefaultSnapshotScheduleKeep)
	}
}

func TestRetention_SnapshotScheduleParsesInterval(t *testing.T) {
	cfg, err := loadFragment(t, "[retention]\nsnapshot_schedule_interval = \"6h\"\nsnapshot_schedule_keep = 12\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Retention.SnapshotScheduleInterval != 6*time.Hour {
		t.Fatalf("interval = %s, want 6h", cfg.Retention.SnapshotScheduleInterval)
	}
	if cfg.Retention.SnapshotScheduleKeep != 12 {
		t.Fatalf("keep = %d, want 12", cfg.Retention.SnapshotScheduleKeep)
	}
}

// A malformed duration is fatal and names the key. An operator who wrote
// "1hour" must not silently get a disabled feature — which is exactly what a
// permissive parse would give them.
func TestRetention_SnapshotScheduleRejectsAMalformedInterval(t *testing.T) {
	_, err := loadFragment(t, "[retention]\nsnapshot_schedule_interval = \"1hour\"\n")
	if err == nil {
		t.Fatal("a malformed interval should fail startup, not silently disable the feature")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
	if got := err.Error(); !strings.Contains(got, "snapshot_schedule_interval") {
		t.Fatalf("the error must name the offending key: %q", got)
	}
}

// Negative is not "off": 0 is the documented off value, and a negative
// duration can only come from a hand-written config meaning something else.
func TestRetention_SnapshotScheduleRejectsANegativeInterval(t *testing.T) {
	_, err := loadFragment(t, "[retention]\nsnapshot_schedule_interval = \"-1h\"\n")
	if err == nil {
		t.Fatal("a negative interval should be refused rather than treated as off")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestRetention_SnapshotScheduleRejectsANonPositiveKeep(t *testing.T) {
	_, err := loadFragment(t, "[retention]\nsnapshot_schedule_interval = \"1h\"\nsnapshot_schedule_keep = -1\n")
	if err == nil {
		t.Fatal("a negative keep should be refused")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}
