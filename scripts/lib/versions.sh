#!/usr/bin/env bash
# scripts/lib/versions.sh — single source of truth for pinned toolchain
# versions (T-3806).
#
# scripts/ci-local.sh sources this instead of hardcoding GO_VERSION_EXPECTED/
# NODE_MAJOR itself, and packaging/Makefile's release build path (`make -C
# packaging deb`, and packaging/publish-release.sh's `make build` ahead of
# it) reads GO_VERSION_EXPECTED from here too — see docs/development.md's
# "Toolchain pinning" section. Bumping a version means editing exactly this
# file; `grep -rn GO_VERSION_EXPECTED` / `grep -rn NODE_MAJOR` across the
# repo should find only this definition and its consumers assigning from it,
# never a second literal.
#
# Meant to be sourced, not executed: `. scripts/lib/versions.sh`. Each
# assignment honours an existing environment value (so `GO_VERSION_EXPECTED=x
# scripts/ci-local.sh` still works for a deliberate one-off override) rather
# than clobbering it.

GO_VERSION_EXPECTED="${GO_VERSION_EXPECTED:-1.26.6}"
NODE_MAJOR="${NODE_MAJOR:-22}"
