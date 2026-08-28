// SPDX-License-Identifier: Apache-2.0

package sigstoreverify

import "github.com/sigstore/sigstore-go/pkg/verify"

// Identity is the expected Fulcio certificate identity a key attestation
// must carry — the trust anchor an operator supplies on the command line
// (vnproxctl hub verify --sigstore-issuer/--sigstore-identity, and their
// -regexp forms), never inferred from the certificate itself. Issuer and
// SAN each accept an exact value, a regexp, or both; sigstore-go's own
// NewShortCertificateIdentity requires at least one of the two forms for
// each.
type Identity struct {
	Issuer       string
	IssuerRegexp string
	SAN          string
	SANRegexp    string
}

// Empty reports whether no identity criteria were configured at all — the
// zero value, which must never be treated as "match anything."
func (i Identity) Empty() bool {
	return i.Issuer == "" && i.IssuerRegexp == "" && i.SAN == "" && i.SANRegexp == ""
}

func (i Identity) certificateIdentity() (verify.CertificateIdentity, error) {
	return verify.NewShortCertificateIdentity(i.Issuer, i.IssuerRegexp, i.SAN, i.SANRegexp)
}
