// installer_test.go covers T-2801 AC4 and AC5 against the real
// packaging/install.sh:
//
//	AC4 "The installer refuses a bad signature and exits non-zero; asserted
//	     against a deliberately corrupted artifact."
//	AC5 "The installer is idempotent — running it twice leaves the same
//	     versions and no duplicate sources entry."
//
// Everything runs over file:// into a temp prefix: no root, no container,
// no network, no port. What that buys is that these run on every `make
// check`; what it costs is stated in the file's last test and in this
// task's report — the apt-repository half of AC5 (a real
// /etc/apt/sources.list.d entry) needs a container, and a *published*
// release artifact cannot be verified at all while there is no published
// release.
package installer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fakeVersion = "9.9.9"
	// The fake vnproxd the tarball carries. `install_tarball` reads
	// `vnproxd --version`'s second field to decide whether the prefix is
	// already at the target version, which is what AC5 turns on.
	fakeDaemon = "#!/bin/sh\necho \"vnproxd " + fakeVersion + "\"\n"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	return abs
}

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not on PATH; install.sh's signature verification cannot be exercised here", name)
	}
}

// signingKey is a throwaway GPG identity, generated per test in its own
// GNUPGHOME — the same approach packaging/build-apt-repo.sh already takes
// for its dev/test signing.
type signingKey struct {
	t      *testing.T
	home   string
	pubKey string
}

func newSigningKey(t *testing.T, uid string) *signingKey {
	t.Helper()
	requireTool(t, "gpg")
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatalf("chmod GNUPGHOME: %v", err)
	}
	k := &signingKey{home: home, t: t}

	batch := filepath.Join(home, "batch")
	body := fmt.Sprintf(`%%no-protection
Key-Type: eddsa
Key-Curve: Ed25519
Name-Real: %s
Name-Comment: ephemeral, test-only
Name-Email: test@vnprox.invalid
Expire-Date: 1d
%%commit
`, uid)
	if err := os.WriteFile(batch, []byte(body), 0o600); err != nil {
		t.Fatalf("writing gpg batch: %v", err)
	}
	k.run("--batch", "--gen-key", batch)

	pub := filepath.Join(home, "pub.asc")
	out := k.output("--batch", "--armor", "--export")
	if strings.TrimSpace(out) == "" {
		t.Fatal("gpg exported an empty public key")
	}
	if err := os.WriteFile(pub, []byte(out), 0o600); err != nil {
		t.Fatalf("writing public key: %v", err)
	}
	k.pubKey = pub
	return k
}

func (k *signingKey) cmd(args ...string) *exec.Cmd {
	c := exec.Command("gpg", args...)
	c.Env = append(os.Environ(), "GNUPGHOME="+k.home)
	return c
}

func (k *signingKey) run(args ...string) {
	k.t.Helper()
	if out, err := k.cmd(args...).CombinedOutput(); err != nil {
		k.t.Fatalf("gpg %v: %v\n%s", args, err, out)
	}
}

func (k *signingKey) output(args ...string) string {
	k.t.Helper()
	out, err := k.cmd(args...).Output()
	if err != nil {
		k.t.Fatalf("gpg %v: %v", args, err)
	}
	return string(out)
}

// sign writes a detached armored signature at <path>.asc.
func (k *signingKey) sign(path string) {
	k.t.Helper()
	k.run("--batch", "--yes", "--armor", "--detach-sign", "--output", path+".asc", path)
}

// distDir builds a fake distribution tree: latest.txt, a tarball carrying a
// fake vnproxd, and a detached signature over it.
func distDir(t *testing.T, k *signingKey, arch string) string {
	t.Helper()
	requireTool(t, "tar")
	dist := t.TempDir()

	stage := filepath.Join(t.TempDir(), "vnprox-"+fakeVersion)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatalf("mkdir stage: %v", err)
	}
	for _, name := range []string{"vnproxd", "vnproxctl", "vnprox-setup"} {
		body := fakeDaemon
		if name != "vnproxd" {
			body = "#!/bin/sh\necho \"" + name + " " + fakeVersion + "\"\n"
		}
		if err := os.WriteFile(filepath.Join(stage, name), []byte(body), 0o755); err != nil {
			t.Fatalf("writing fake %s: %v", name, err)
		}
	}

	tarball := filepath.Join(dist, "vnprox_"+fakeVersion+"_"+arch+".tar.gz")
	cmd := exec.Command("tar", "-czf", tarball, "-C", filepath.Dir(stage), filepath.Base(stage))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tar: %v\n%s", err, out)
	}
	k.sign(tarball)

	if err := os.WriteFile(filepath.Join(dist, "latest.txt"), []byte(fakeVersion+"\n"), 0o600); err != nil {
		t.Fatalf("writing latest.txt: %v", err)
	}
	return dist
}

// hostArch is what install.sh's detect_arch will resolve to, so the test's
// tarball is named the thing the script will ask for.
func hostArch(t *testing.T) string {
	t.Helper()
	if out, err := exec.Command("dpkg", "--print-architecture").Output(); err == nil {
		if a := strings.TrimSpace(string(out)); a == "amd64" || a == "arm64" {
			return a
		}
	}
	out, err := exec.Command("uname", "-m").Output()
	if err != nil {
		t.Skipf("cannot determine host architecture: %v", err)
	}
	switch strings.TrimSpace(string(out)) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		t.Skipf("install.sh ships amd64/arm64 only; this host is %s", strings.TrimSpace(string(out)))
		return ""
	}
}

type runResult struct {
	stdout string
	stderr string
	code   int
}

func runInstaller(t *testing.T, args ...string) runResult {
	t.Helper()
	requireTool(t, "bash")
	cmd := exec.Command("bash", append([]string{filepath.Join(repoRoot(t), "packaging", "install.sh")}, args...)...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	if err != nil {
		if !asExitError(err, &exitErr) {
			t.Fatalf("running install.sh: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return runResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

func asExitError(err error, target **exec.ExitError) bool {
	if e, ok := err.(*exec.ExitError); ok { //nolint:errorlint // exec.Cmd.Run returns this directly
		*target = e
		return true
	}
	return false
}

// The happy path, and the control leg for every refusal below: a correctly
// signed artifact from a trusted key installs. Without this, "it refused"
// would be indistinguishable from "it refuses everything".
func TestInstaller_InstallsACorrectlySignedTarball(t *testing.T) {
	k := newSigningKey(t, "vnprox test release key")
	arch := hostArch(t)
	dist := distDir(t, k, arch)
	prefix := t.TempDir()

	res := runInstaller(t,
		"--prefix", prefix,
		"--dist-url", "file://"+dist,
		"--release-key", k.pubKey,
		"--yes", "--no-lldp", "--skip-pve-check")
	if res.code != 0 {
		t.Fatalf("install.sh exited %d\nstdout:\n%s\nstderr:\n%s", res.code, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stderr, "signature verified") {
		t.Errorf("install.sh did not report verifying a signature; stderr:\n%s", res.stderr)
	}
	for _, name := range []string{"vnproxd", "vnproxctl", "vnprox-setup"} {
		if _, err := os.Stat(filepath.Join(prefix, "bin", name)); err != nil {
			t.Errorf("%s was not installed: %v", name, err)
		}
	}
}

// AC4. A deliberately corrupted artifact — the exact bytes the signature
// was made over, with one changed.
func TestInstaller_RefusesACorruptedArtifact(t *testing.T) {
	k := newSigningKey(t, "vnprox test release key")
	arch := hostArch(t)
	dist := distDir(t, k, arch)
	prefix := t.TempDir()

	tarball := filepath.Join(dist, "vnprox_"+fakeVersion+"_"+arch+".tar.gz")
	data, err := os.ReadFile(tarball)
	if err != nil {
		t.Fatalf("reading tarball: %v", err)
	}
	// Flip a byte in the middle. The signature stays exactly as it was, so
	// this is precisely "the artifact does not match its signature".
	data[len(data)/2] ^= 0xFF
	if err := os.WriteFile(tarball, data, 0o600); err != nil {
		t.Fatalf("corrupting tarball: %v", err)
	}

	res := runInstaller(t,
		"--prefix", prefix,
		"--dist-url", "file://"+dist,
		"--release-key", k.pubKey,
		"--yes", "--no-lldp", "--skip-pve-check")
	if res.code == 0 {
		t.Fatal("install.sh exited 0 on a corrupted artifact; it must refuse and exit non-zero")
	}
	if !strings.Contains(res.stderr, "signature verification FAILED") {
		t.Errorf("the refusal does not say what happened; stderr:\n%s", res.stderr)
	}
	// And nothing was installed. A script that refused loudly and unpacked
	// anyway would pass an exit-code-only assertion.
	if _, err := os.Stat(filepath.Join(prefix, "bin", "vnproxd")); err == nil {
		t.Error("install.sh installed the binaries despite refusing the signature")
	}
}

// A valid signature by the WRONG key. Distinct from corruption: the
// artifact and its signature agree perfectly, and the only thing wrong is
// who signed it. An implementation that ran `gpg --verify` without
// controlling the keyring would pass the corruption test and fail this one.
func TestInstaller_RefusesAValidSignatureByAnUntrustedKey(t *testing.T) {
	trusted := newSigningKey(t, "vnprox test release key")
	attacker := newSigningKey(t, "not the vnprox release key")
	arch := hostArch(t)
	dist := distDir(t, attacker, arch)
	prefix := t.TempDir()

	res := runInstaller(t,
		"--prefix", prefix,
		"--dist-url", "file://"+dist,
		"--release-key", trusted.pubKey,
		"--yes", "--no-lldp", "--skip-pve-check")
	if res.code == 0 {
		t.Fatal("install.sh accepted an artifact signed by an untrusted key")
	}
	if !strings.Contains(res.stderr, "signature verification FAILED") {
		t.Errorf("the refusal does not say what happened; stderr:\n%s", res.stderr)
	}
}

// A missing signature is refused too. "Could not check" and "checked and it
// was wrong" have to be the same outcome, or deleting the .asc becomes the
// --insecure this card says must not exist.
func TestInstaller_RefusesAMissingSignature(t *testing.T) {
	k := newSigningKey(t, "vnprox test release key")
	arch := hostArch(t)
	dist := distDir(t, k, arch)
	prefix := t.TempDir()

	if err := os.Remove(filepath.Join(dist, "vnprox_"+fakeVersion+"_"+arch+".tar.gz.asc")); err != nil {
		t.Fatalf("removing signature: %v", err)
	}
	res := runInstaller(t,
		"--prefix", prefix,
		"--dist-url", "file://"+dist,
		"--release-key", k.pubKey,
		"--yes", "--no-lldp", "--skip-pve-check")
	if res.code == 0 {
		t.Fatal("install.sh installed an artifact with no signature at all")
	}
}

// With no trust anchor available at all, the script refuses rather than
// installing unverified. The pinned fingerprint in install.sh is still a
// placeholder (there is no published vnprox release key), so this is the
// state a real `curl | sh` is in today — and it fails closed.
// T-3301 pinned a real production fingerprint into the shipped script (see
// TestInstaller_PinnedKeyIsStillAPlaceholder's own doc comment — that test
// now documents this), so the no-trust-anchor scenario can no longer be
// reached through install.sh's own shipped default. This test forces it on
// a copy instead, exactly the way TestInstaller_AcceptsA.../
// TestInstaller_RefusesAFetchedKeyWithTheWrongFingerprint pin a REAL
// fingerprint onto a copy for their own scenarios — pinFingerprint's own
// doc comment names this as the intended pattern.
func TestInstaller_RefusesWhenNoTrustAnchorIsAvailable(t *testing.T) {
	k := newSigningKey(t, "vnprox test release key")
	arch := hostArch(t)
	dist := distDir(t, k, arch)
	script := pinFingerprint(t, "0000000000000000000000000000000000000000")
	prefix := t.TempDir()

	res := runScript(t, script,
		"--prefix", prefix,
		"--dist-url", "file://"+dist,
		"--yes", "--no-lldp", "--skip-pve-check")
	if res.code == 0 {
		t.Fatal("install.sh installed a downloaded artifact with no trust anchor")
	}
	if !strings.Contains(res.stderr, "carries no release-key fingerprint") {
		t.Errorf("the refusal does not explain the missing trust anchor; stderr:\n%s", res.stderr)
	}
}

// AC5. Two runs, same versions, and the second one knows it has nothing to
// do rather than reinstalling.
func TestInstaller_IsIdempotent(t *testing.T) {
	k := newSigningKey(t, "vnprox test release key")
	arch := hostArch(t)
	dist := distDir(t, k, arch)
	prefix := t.TempDir()

	args := []string{
		"--prefix", prefix,
		"--dist-url", "file://" + dist,
		"--release-key", k.pubKey,
		"--yes", "--no-lldp", "--skip-pve-check",
	}

	first := runInstaller(t, args...)
	if first.code != 0 {
		t.Fatalf("first run exited %d\n%s", first.code, first.stderr)
	}
	daemon := filepath.Join(prefix, "bin", "vnproxd")
	firstInfo, err := os.Stat(daemon)
	if err != nil {
		t.Fatalf("stat after first run: %v", err)
	}
	firstVersion := versionOf(t, daemon)

	second := runInstaller(t, args...)
	if second.code != 0 {
		t.Fatalf("second run exited %d\n%s", second.code, second.stderr)
	}
	if !strings.Contains(second.stderr, "already installed") {
		t.Errorf("the second run did not recognise the existing install; stderr:\n%s", second.stderr)
	}
	if got := versionOf(t, daemon); got != firstVersion {
		t.Errorf("version changed across two identical runs: %q then %q", firstVersion, got)
	}
	secondInfo, err := os.Stat(daemon)
	if err != nil {
		t.Fatalf("stat after second run: %v", err)
	}
	// Not re-extracted, so not rewritten. Stronger than "the version is the
	// same", which a reinstall of the same version would also satisfy.
	if !firstInfo.ModTime().Equal(secondInfo.ModTime()) {
		t.Errorf("the second run rewrote %s (mtime %v -> %v); an idempotent run has nothing to do",
			daemon, firstInfo.ModTime(), secondInfo.ModTime())
	}
}

// stripShellComments removes whole-line `#` comments. Not a shell parser
// and does not need to be: a flag can only be *implemented* in code, and
// code is what is left.
func stripShellComments(s string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func versionOf(t *testing.T, binary string) string {
	t.Helper()
	out, err := exec.Command(binary, "--version").Output()
	if err != nil {
		t.Fatalf("%s --version: %v", binary, err)
	}
	return strings.TrimSpace(string(out))
}

// "Signature verification is not skippable; there is no --insecure."
//
// A static assertion over the script's own text, and the only guard here
// that survives someone deciding a bypass would be convenient. The other
// tests prove the current implementation refuses; this one refuses the
// FLAG, which is what the card actually forbids.
func TestInstaller_OffersNoWayToSkipVerification(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "packaging", "install.sh"))
	if err != nil {
		t.Fatalf("reading install.sh: %v", err)
	}
	// Comment lines are stripped first. This file's own prose — and
	// install.sh's — has to be able to NAME the flags that must not exist in
	// order to explain why, and a scan that could not tell an explanation
	// from an implementation would make the guard unmaintainable.
	text := stripShellComments(string(body))
	for _, forbidden := range []string{
		"--insecure",
		"--no-verify",
		"--skip-verify",
		"--skip-signature",
		"--no-signature",
		"--allow-unsigned",
		"VNPROX_SKIP_VERIFY",
		"VNPROX_INSECURE",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("packaging/install.sh mentions %q — signature verification must not be skippable", forbidden)
		}
	}
	// curl's own --insecure would disable TLS verification on the download,
	// which is the same hole wearing a different hat.
	for _, forbidden := range []string{"curl -k", "curl --insecure", "-fsSLk"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("packaging/install.sh uses %q — TLS verification must not be disabled either", forbidden)
		}
	}
}

// --- the pinned-fingerprint path -------------------------------------------
//
// Everything above supplies the trust anchor with --release-key, which is
// the air-gapped/test shape. The shape a real `curl | sh` takes is the
// other one: fetch the distribution's published key and accept it ONLY if
// its fingerprint matches the one install.sh carries. That path is
// unreachable while the pinned fingerprint is a placeholder, so the tests
// below pin one — by performing exactly the substitution the release
// procedure performs, on a copy, through the marker comment install.sh
// carries for that purpose. Testing the release procedure's output is the
// point; hand-editing something else would not be.

// pinFingerprint copies install.sh with VNPROX_RELEASE_KEY_FPR replaced,
// the way a release job would, and returns the copy's path.
func pinFingerprint(t *testing.T, fpr string) string {
	t.Helper()
	src := filepath.Join(repoRoot(t), "packaging", "install.sh")
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading install.sh: %v", err)
	}
	const marker = "# vnprox-release-key-fingerprint {{{\n"
	const endMarker = "\n# }}} vnprox-release-key-fingerprint"
	text := string(body)
	start := strings.Index(text, marker)
	end := strings.Index(text, endMarker)
	if start < 0 || end < 0 || end < start {
		t.Fatal("packaging/install.sh no longer carries the vnprox-release-key-fingerprint markers; the release procedure cannot substitute the pinned key mechanically")
	}
	replaced := text[:start+len(marker)] + "VNPROX_RELEASE_KEY_FPR=\"" + fpr + "\"" + text[end:]
	out := filepath.Join(t.TempDir(), "install.sh")
	if writeErr := os.WriteFile(out, []byte(replaced), 0o700); writeErr != nil {
		t.Fatalf("writing the pinned copy: %v", writeErr)
	}
	return out
}

func runScript(t *testing.T, script string, args ...string) runResult {
	t.Helper()
	requireTool(t, "bash")
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	if err != nil {
		if !asExitError(err, &exitErr) {
			t.Fatalf("running %s: %v", script, err)
		}
		code = exitErr.ExitCode()
	}
	return runResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// fingerprint reads the primary key fingerprint out of a signing key's own
// keyring — the value a release job would pin.
func (k *signingKey) fingerprint() string {
	k.t.Helper()
	out := k.output("--batch", "--with-colons", "--fingerprint")
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) > 9 && fields[0] == "fpr" {
			return fields[9]
		}
	}
	k.t.Fatal("could not read a fingerprint from the test key")
	return ""
}

// publishKey writes the distribution's public key where install.sh looks
// for it.
func publishKey(t *testing.T, dist string, k *signingKey) {
	t.Helper()
	body, err := os.ReadFile(k.pubKey)
	if err != nil {
		t.Fatalf("reading the public key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "vnprox-release-key.asc"), body, 0o600); err != nil {
		t.Fatalf("publishing the key: %v", err)
	}
}

// The published key is fetched and accepted because its fingerprint matches
// the pinned one. This is the happy path of a real `curl | sh`.
func TestInstaller_AcceptsAFetchedKeyMatchingThePinnedFingerprint(t *testing.T) {
	k := newSigningKey(t, "vnprox test release key")
	arch := hostArch(t)
	dist := distDir(t, k, arch)
	publishKey(t, dist, k)
	script := pinFingerprint(t, k.fingerprint())
	prefix := t.TempDir()

	res := runScript(t, script,
		"--prefix", prefix,
		"--dist-url", "file://"+dist,
		"--yes", "--no-lldp", "--skip-pve-check")
	if res.code != 0 {
		t.Fatalf("install.sh exited %d with a correctly pinned key\nstderr:\n%s", res.code, res.stderr)
	}
	if _, err := os.Stat(filepath.Join(prefix, "bin", "vnproxd")); err != nil {
		t.Errorf("nothing was installed: %v", err)
	}
}

// The refusal that makes pinning worth anything: the distribution serves a
// perfectly valid key, and its signature over the artifact verifies — but it
// is not the pinned key, so the whole thing is refused before a byte is
// unpacked. Without this check, "the key came from the same host as the
// package" would be the entire trust model.
func TestInstaller_RefusesAFetchedKeyWithTheWrongFingerprint(t *testing.T) {
	attacker := newSigningKey(t, "not the vnprox release key")
	expected := newSigningKey(t, "vnprox test release key")
	arch := hostArch(t)
	dist := distDir(t, attacker, arch)
	publishKey(t, dist, attacker) // consistent: their key, their signature
	script := pinFingerprint(t, expected.fingerprint())
	prefix := t.TempDir()

	res := runScript(t, script,
		"--prefix", prefix,
		"--dist-url", "file://"+dist,
		"--yes", "--no-lldp", "--skip-pve-check")
	if res.code == 0 {
		t.Fatal("install.sh accepted a fetched key whose fingerprint is not the pinned one")
	}
	if !strings.Contains(res.stderr, "is not the one this installer pins") {
		t.Errorf("the refusal does not name the fingerprint mismatch; stderr:\n%s", res.stderr)
	}
	if _, err := os.Stat(filepath.Join(prefix, "bin", "vnproxd")); err == nil {
		t.Error("install.sh installed the binaries despite refusing the key")
	}
}

// T-3301 (2026-08-18): the release-key fingerprint has been pinned for
// real — this replaces the old TestInstaller_PinnedKeyIsStillAPlaceholder,
// exactly as that test's own doc comment asked for ("delete this test, and
// add one asserting the published key's fingerprint instead"). This
// doesn't reach the real apt.vnprox.com (no network access assumed for a
// unit test, and it doesn't resolve publicly yet regardless — see
// packaging/apt-repo.md's Status), so what it can and does check is
// internal consistency: the fingerprint install.sh carries matches the one
// this project publishes out-of-band (packaging/apt-repo.md's Signing key
// section) — catching a corrupted or mistyped pin, which a `curl | sh`
// install has no other way to notice before it's too late to matter.
func TestInstaller_PinnedFingerprintMatchesPublished(t *testing.T) {
	const published = "F57DDE63ABA03B3BEEEB2DB93BD9CC3B118061BD"
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "packaging", "install.sh"))
	if err != nil {
		t.Fatalf("reading install.sh: %v", err)
	}
	want := `VNPROX_RELEASE_KEY_FPR="` + published + `"`
	if !strings.Contains(string(body), want) {
		t.Errorf("install.sh does not carry the published fingerprint %s (packaging/apt-repo.md's Signing key section) — "+
			"either it was rotated and this test needs updating, or it was corrupted and a real `curl | sh` install "+
			"would refuse to trust the real key", published)
	}
}
