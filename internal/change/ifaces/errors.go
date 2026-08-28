// SPDX-License-Identifier: Apache-2.0

package ifaces

import "errors"

// ErrNotFound is returned by Mutate when an update/delete/port op's target
// iface stanza does not exist in the file being mutated.
var ErrNotFound = errors.New("ifaces: target iface not found")

// ErrExists is returned by Mutate when a create op's target name already
// has an iface stanza in the file being mutated.
var ErrExists = errors.New("ifaces: target iface already exists")
