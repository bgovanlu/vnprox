// SPDX-License-Identifier: Apache-2.0

package gitsync

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
)

// This file implements verification of git's SSH-format commit signatures
// (`gpg.format = ssh`, git >= 2.34, supported by GitHub and GitLab) against
// an operator-maintained allowed-signers file — with the standard library
// only. OpenPGP would need a third-party dependency (x/crypto/openpgp is
// frozen and deprecated), and adding one to check a signature would trade
// away exactly the property this card's git-access decision was made to
// keep. An OpenPGP-signed commit therefore reports as *unverifiable* and,
// under require_signed_commits, is refused — fail-closed, and named as such
// in the finding, rather than quietly accepted.
//
// The wire format is OpenSSH's PROTOCOL.sshsig:
//
//	blob        := "SSHSIG" || uint32 version || string publickey ||
//	               string namespace || string reserved ||
//	               string hash_algorithm || string signature
//	signed data := "SSHSIG" || string namespace || string reserved ||
//	               string hash_algorithm || string H(message)
//
// git signs with namespace "git".

const (
	sshsigMagic       = "SSHSIG"
	sshsigVersion     = 1
	sshsigGitNamespac = "git"

	sshsigBeginArmor = "-----BEGIN SSH SIGNATURE-----"
	sshsigEndArmor   = "-----END SSH SIGNATURE-----"
)

// AllowedSigner is one trusted signing key from an allowed-signers file.
type AllowedSigner struct {
	// Principal is the identity column (an email address, typically). It is
	// carried so a finding can name *who* signed, and is never used as a
	// trust decision on its own.
	Principal string
	// KeyType is the SSH key algorithm name, e.g. "ssh-ed25519".
	KeyType string
	// Blob is the raw SSH public-key blob (the decoded base64 field).
	Blob []byte
}

// LoadAllowedSigners parses an OpenSSH allowed-signers file (the format
// `ssh-keygen -Y verify -f` reads) or a plain authorized_keys-style file.
// Both shapes are accepted because the distinguishing column — the leading
// principal list — is simply absent in the second, and refusing the file an
// operator is most likely to already have would buy nothing.
//
// A file that exists but yields zero usable keys is an error, not an empty
// trust set: "no trusted signers" and "every signature is unverifiable" must
// never be reachable by a typo.
func LoadAllowedSigners(path string) ([]AllowedSigner, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-configured trust anchor path, same convention as [peer] ca_file
	if err != nil {
		return nil, fmt.Errorf("gitsync: reading allowed-signers file %s: %w", path, err)
	}
	var out []AllowedSigner
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		signer, ok := parseAllowedSignerLine(line)
		if ok {
			out = append(out, signer)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("gitsync: allowed-signers file %s contains no usable ssh public key", path)
	}
	return out, nil
}

// supportedKeyTypes is the closed set of SSH key algorithms this package can
// verify with the standard library. A signature under anything else is
// unverifiable — refused, never assumed good.
var supportedKeyTypes = map[string]bool{
	"ssh-ed25519": true,
	"ssh-rsa":     true,
}

// parseAllowedSignerLine finds the (keytype, base64) pair in a line,
// whatever columns precede it. Returns ok=false for a line naming a key type
// this package cannot verify, so an operator's mixed-algorithm file loads
// its usable half rather than failing wholesale.
func parseAllowedSignerLine(line string) (AllowedSigner, bool) {
	fields := strings.Fields(line)
	for i, f := range fields {
		if !supportedKeyTypes[f] || i+1 >= len(fields) {
			continue
		}
		blob, err := base64.StdEncoding.DecodeString(fields[i+1])
		if err != nil {
			return AllowedSigner{}, false
		}
		principal := ""
		if i > 0 {
			principal = fields[0]
		}
		return AllowedSigner{Principal: principal, KeyType: f, Blob: blob}, true
	}
	return AllowedSigner{}, false
}

// sshReader reads OpenSSH wire-format values out of a byte slice.
type sshReader struct{ b []byte }

func (r *sshReader) uint32() (uint32, error) {
	if len(r.b) < 4 {
		return 0, errors.New("truncated uint32")
	}
	v := binary.BigEndian.Uint32(r.b[:4])
	r.b = r.b[4:]
	return v, nil
}

func (r *sshReader) str() ([]byte, error) {
	n, err := r.uint32()
	if err != nil {
		return nil, err
	}
	if uint64(n) > uint64(len(r.b)) {
		return nil, errors.New("truncated string")
	}
	v := r.b[:n]
	r.b = r.b[n:]
	return v, nil
}

func sshString(b []byte) []byte {
	out := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(out[:4], uint32(len(b))) //nolint:gosec // lengths here are bounded by maxSpecBytes
	copy(out[4:], b)
	return out
}

// VerifyCommit reports whether sig is a valid SSH-format git commit
// signature over payload, made by one of signers. It returns the matching
// signer's principal on success.
//
// Everything is fail-closed: an unparseable armor block, an unexpected
// namespace, an unsupported algorithm, a signer not in the list, or a bad
// signature all return an error wrapping ErrUnverifiableSignature. There is
// no code path where "could not determine" is reported as verified.
//
// Residual, stated rather than hidden (see this package's doc comment): the
// payload is supplied by the host. Verifying it proves the *commit object*
// was signed by a trusted key; it does not, on its own, prove the separately
// fetched file content is the blob under that commit's tree — establishing
// that would require the git object protocol this card deliberately did not
// take on. An operator who needs that binding should treat the host as part
// of their trust boundary, which is the same position `git clone` over
// HTTPS puts them in.
func VerifyCommit(payload []byte, armored string, signers []AllowedSigner) (principal string, err error) {
	blob, err := decodeSSHSIGArmor(armored)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnverifiableSignature, err)
	}

	if !bytes.HasPrefix(blob, []byte(sshsigMagic)) {
		return "", fmt.Errorf("%w: signature is not an SSHSIG blob (an OpenPGP-signed commit cannot be verified by this daemon)", ErrUnverifiableSignature)
	}
	r := &sshReader{b: blob[len(sshsigMagic):]}

	version, err := r.uint32()
	if err != nil || version != sshsigVersion {
		return "", fmt.Errorf("%w: unsupported SSHSIG version", ErrUnverifiableSignature)
	}
	pubBlob, err := r.str()
	if err != nil {
		return "", fmt.Errorf("%w: malformed public key field", ErrUnverifiableSignature)
	}
	namespace, err := r.str()
	if err != nil {
		return "", fmt.Errorf("%w: malformed namespace field", ErrUnverifiableSignature)
	}
	if string(namespace) != sshsigGitNamespac {
		// A signature made for a different namespace (a file signature, an
		// email signature) must never authenticate a commit — that is the
		// whole reason SSHSIG has a namespace.
		return "", fmt.Errorf("%w: signature namespace is %q, want %q", ErrUnverifiableSignature, namespace, sshsigGitNamespac)
	}
	reserved, err := r.str()
	if err != nil {
		return "", fmt.Errorf("%w: malformed reserved field", ErrUnverifiableSignature)
	}
	hashAlg, err := r.str()
	if err != nil {
		return "", fmt.Errorf("%w: malformed hash-algorithm field", ErrUnverifiableSignature)
	}
	sigBlob, err := r.str()
	if err != nil {
		return "", fmt.Errorf("%w: malformed signature field", ErrUnverifiableSignature)
	}

	// The signer must be one the operator listed. Comparing the whole key
	// blob means a key of the right algorithm but the wrong value cannot
	// match, and a principal string is never the thing trusted.
	match, ok := matchSigner(pubBlob, signers)
	if !ok {
		return "", fmt.Errorf("%w: the signing key is not in the allowed-signers file", ErrUnverifiableSignature)
	}

	digest, err := hashPayload(string(hashAlg), payload)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnverifiableSignature, err)
	}

	signed := bytes.NewBuffer(nil)
	signed.WriteString(sshsigMagic)
	signed.Write(sshString(namespace))
	signed.Write(sshString(reserved))
	signed.Write(sshString(hashAlg))
	signed.Write(sshString(digest))

	if err := verifySSHSignature(pubBlob, sigBlob, signed.Bytes()); err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnverifiableSignature, err)
	}
	return match.Principal, nil
}

func matchSigner(pubBlob []byte, signers []AllowedSigner) (AllowedSigner, bool) {
	for _, s := range signers {
		if bytes.Equal(s.Blob, pubBlob) {
			return s, true
		}
	}
	return AllowedSigner{}, false
}

func hashPayload(alg string, payload []byte) ([]byte, error) {
	switch alg {
	case "sha256":
		sum := sha256.Sum256(payload)
		return sum[:], nil
	case "sha512":
		sum := sha512.Sum512(payload)
		return sum[:], nil
	default:
		return nil, fmt.Errorf("unsupported hash algorithm %q", alg)
	}
}

// decodeSSHSIGArmor strips the BEGIN/END lines and base64-decodes the body.
func decodeSSHSIGArmor(armored string) ([]byte, error) {
	s := strings.TrimSpace(armored)
	if !strings.HasPrefix(s, sshsigBeginArmor) {
		return nil, errors.New("missing SSH SIGNATURE armor header")
	}
	end := strings.Index(s, sshsigEndArmor)
	if end < 0 {
		return nil, errors.New("missing SSH SIGNATURE armor footer")
	}
	body := s[len(sshsigBeginArmor):end]
	body = strings.Join(strings.Fields(body), "")
	blob, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return nil, fmt.Errorf("decoding armored signature: %w", err)
	}
	return blob, nil
}

// verifySSHSignature dispatches on the public key's own algorithm, then
// checks that the signature blob's algorithm belongs to that key type. A
// signature claiming an algorithm the key cannot produce is refused rather
// than reinterpreted.
func verifySSHSignature(pubBlob, sigBlob, signed []byte) error {
	pr := &sshReader{b: pubBlob}
	keyType, err := pr.str()
	if err != nil {
		return errors.New("malformed public key blob")
	}
	sr := &sshReader{b: sigBlob}
	sigAlg, err := sr.str()
	if err != nil {
		return errors.New("malformed signature blob")
	}
	sigBytes, err := sr.str()
	if err != nil {
		return errors.New("malformed signature value")
	}

	switch string(keyType) {
	case "ssh-ed25519":
		if string(sigAlg) != "ssh-ed25519" {
			return fmt.Errorf("signature algorithm %q does not match an ed25519 key", sigAlg)
		}
		raw, err := pr.str()
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return errors.New("malformed ed25519 public key")
		}
		if !ed25519.Verify(ed25519.PublicKey(raw), signed, sigBytes) {
			return errors.New("ed25519 signature does not verify")
		}
		return nil
	case "ssh-rsa":
		pub, err := parseRSAPublicKey(pr)
		if err != nil {
			return err
		}
		var hash crypto.Hash
		switch string(sigAlg) {
		case "rsa-sha2-256":
			hash = crypto.SHA256
		case "rsa-sha2-512":
			hash = crypto.SHA512
		default:
			// SHA-1 ("ssh-rsa") is deliberately not accepted.
			return fmt.Errorf("unsupported RSA signature algorithm %q", sigAlg)
		}
		h := hash.New()
		h.Write(signed)
		if err := rsa.VerifyPKCS1v15(pub, hash, h.Sum(nil), sigBytes); err != nil {
			return fmt.Errorf("rsa signature does not verify: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported signing key type %q", keyType)
	}
}

func parseRSAPublicKey(r *sshReader) (*rsa.PublicKey, error) {
	eBytes, err := r.str()
	if err != nil {
		return nil, errors.New("malformed rsa exponent")
	}
	nBytes, err := r.str()
	if err != nil {
		return nil, errors.New("malformed rsa modulus")
	}
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() || e.Int64() < 3 {
		return nil, errors.New("implausible rsa exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e.Int64())}, nil
}
