// SPDX-License-Identifier: Apache-2.0

// Package installer holds no code. It exists so packaging/install.sh's
// signature verification and idempotence have a home inside `go test ./...`,
// and therefore inside `make check`.
//
// WHY NOT A SHELL TEST UNDER packaging/test/. Every script there drives a
// real `.deb` inside a podman container with real apt-get and real network
// access — the right shape for "does the package install on Debian", and the
// wrong shape for "does this refuse a corrupted artifact". That refusal is a
// pure function of a few hundred bytes and a gpg invocation; making it
// depend on `make deb`, a container runtime and an internet connection would
// mean it ran rarely, and a guard that runs rarely is a guard that is
// discovered broken late.
//
// The tests here drive the real packaging/install.sh with bash, over
// file:// URLs, into a temporary prefix. No root, no container, no network,
// no port to register.
package installer
