// SPDX-License-Identifier: Apache-2.0

package config

import "errors"

var (
	// ErrInvalidConfig marks a structurally or semantically invalid config
	// value (bad listen address, bad duration, mismatched TLS override, ...).
	ErrInvalidConfig = errors.New("invalid config")
	// ErrTLSCertMissing marks a TLS certificate or key file that could not
	// be found on disk, whether the default PVE certificate or an explicit
	// override.
	ErrTLSCertMissing = errors.New("tls certificate not found")
)
