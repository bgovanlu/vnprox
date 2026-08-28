# packaging/Makefile — builds the vnprox .deb via dpkg-deb (no nfpm
# dependency; dpkg-deb is present on every Debian/Ubuntu build host and on
# the target itself). Invoked by the root Makefile's `deb` target:
#
#   make -C packaging deb DIST_DIR=<abs path to repo>/dist
#
# See planning/tasks/phase-0.md#T-006 and docs/deployment.md.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c

GO ?= go

PKG_NAME    := vnprox
ROOT_DIR    := $(abspath ..)
DIST_DIR    ?= $(ROOT_DIR)/dist
BUILD_DIR   := $(abspath build)
PKG_ROOT    := $(BUILD_DIR)/pkgroot

# Version: see version.sh for the tag-vs-no-tag normalization rules.
VERSION := $(shell ./version.sh $(ROOT_DIR))
ARCH    := $(shell dpkg --print-architecture 2>/dev/null || echo amd64)
DEB_FILE := $(DIST_DIR)/$(PKG_NAME)_$(VERSION)_$(ARCH).deb

# --- T-3806: reproducible builds ------------------------------------------
#
# GO_VERSION_EXPECTED: scripts/lib/versions.sh is the single source of truth
# (docs/development.md "Toolchain pinning") — read from there rather than
# re-pinned as a second literal here. A mismatch is a warning, not a hard
# failure: this Makefile is also invoked directly by developers/hardware
# validation off whatever `go` is on PATH, and docs/development.md already
# says the dev host's own `go version` is what actually matters since
# GitHub Actions is disabled — this warning is the same non-fatal shape as
# scripts/ci-local.sh's own check.
GO_VERSION_EXPECTED := $(shell . $(ROOT_DIR)/scripts/lib/versions.sh && echo "$$GO_VERSION_EXPECTED")
GO_VERSION_ACTUAL    := $(shell $(GO) version 2>/dev/null | awk '{print $$3}' | sed 's/^go//')

# SOURCE_DATE_EPOCH: defaults to the checked-out commit's own timestamp, not
# wall-clock "now" — the whole point is that two builds of the SAME commit,
# run seconds or months apart, embed the same timestamp. dpkg-deb >= 1.18.11
# honours this natively (see `man dpkg-deb`): it stamps the ar(5) container
# with it and CLAMPS every tar(5) member's mtime down to it. "Clamp" (not
# "set") is what makes this work without touching every installed file's
# mtime by hand: a build's real install-time mtime is always later than the
# commit that produced it, so the clamp always fires. Exported so dpkg-deb
# (invoked below) picks it up from the environment; also honoured by any
# caller (e.g. verify-reproducible.sh) that wants a fixed epoch for both of
# a pair of builds instead of independently re-deriving "HEAD's commit time"
# twice (immaterial for a single commit, but explicit is cheaper than
# relying on it).
SOURCE_DATE_EPOCH ?= $(shell git -C $(ROOT_DIR) log -1 --format=%ct)
export SOURCE_DATE_EPOCH

.PHONY: deb clean

deb:
	@if [ -n "$(GO_VERSION_EXPECTED)" ] && [ "$(GO_VERSION_ACTUAL)" != "$(GO_VERSION_EXPECTED)" ]; then \
		echo ">> packaging: WARNING: go $(GO_VERSION_ACTUAL), pinned version is $(GO_VERSION_EXPECTED) (scripts/lib/versions.sh) — a build with a different Go toolchain is not guaranteed byte-identical to one built with the pinned version" >&2; \
	fi
	@echo ">> packaging: building $(PKG_NAME) $(VERSION) ($(ARCH)), SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) ($(shell date -u -d @$(SOURCE_DATE_EPOCH) '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null))"
	# Guard against packaging a stale/placeholder web/dist (hardware
	# validation, T-608, found a real deployment silently served the T-005
	# "frontend not yet built" placeholder — vnproxd's go:embed just embeds
	# whatever is on disk under web/dist at build time, staleness and all).
	# This target intentionally does NOT build the frontend itself — the
	# root Makefile's `deb` target does that before invoking this one, and
	# release.yml runs `make build` before this — but if either is skipped
	# (e.g. this Makefile invoked directly), fail loudly here rather than
	# silently ship a broken UI.
	@if [ ! -f "$(ROOT_DIR)/web/dist/index.html" ] || grep -q "frontend not yet built" "$(ROOT_DIR)/web/dist/index.html" 2>/dev/null; then \
		echo ">> packaging: ERROR: $(ROOT_DIR)/web/dist/index.html is missing or is the placeholder page" >&2; \
		echo "   run 'make build' (or 'cd web && npm run build') first — see docs/development.md" >&2; \
		exit 1; \
	fi
	rm -rf $(PKG_ROOT)
	install -d -m 0755 $(PKG_ROOT)/DEBIAN
	install -d -m 0755 $(PKG_ROOT)/usr/bin
	install -d -m 0755 $(PKG_ROOT)/lib/systemd/system
	install -d -m 0755 $(PKG_ROOT)/etc/vnprox
	install -d -m 0755 $(DIST_DIR)

	@echo ">> packaging: building vnproxd and vnproxctl ($(ARCH))"
	# -trimpath: strips this build's absolute GOPATH/GOROOT/module-cache
	# paths from the binary (source-file paths embedded for panics/stack
	# traces, and the debug/gopclntab data), so the same commit built under
	# two different checkout directories or usernames still matches.
	# -buildvcs=false: the version string already comes from -ldflags -X
	# above (itself derived from version.sh's git describe), so there is no
	# need for `go build`'s own VCS auto-stamping (vcs.revision/vcs.time/
	# vcs.modified embedded via runtime/debug.BuildInfo) — leaving it on
	# would make the binary depend on whether the build environment has a
	# usable `git` and a full history to inspect (e.g. a shallow clone or an
	# exported source tarball behaves differently), which is exactly the
	# kind of environment-dependence a reproducible build must not have.
	# CGO_ENABLED=0: vnprox's only C-capable dependency risk is its SQLite
	# driver, and docs/development.md already locks that to
	# modernc.org/sqlite (pure Go, no cgo) for this reason among others —
	# pinning CGO_ENABLED=0 here makes that guarantee explicit at the build
	# invocation instead of implicit in "nothing currently imports cgo", and
	# removes the host C toolchain/libc as a source of non-reproducibility.
	cd $(ROOT_DIR) && CGO_ENABLED=0 GOARCH=$(ARCH) $(GO) build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$(VERSION)" -o $(PKG_ROOT)/usr/bin/vnproxd ./cmd/vnproxd
	cd $(ROOT_DIR) && CGO_ENABLED=0 GOARCH=$(ARCH) $(GO) build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$(VERSION)" -o $(PKG_ROOT)/usr/bin/vnproxctl ./cmd/vnproxctl
	# `go build -o` inherits the host umask (often 0775); Debian policy wants
	# root-owned /usr/bin binaries to be 0755 (dpkg-deb --root-owner-group
	# fixes ownership but not mode).
	chmod 0755 $(PKG_ROOT)/usr/bin/vnproxd $(PKG_ROOT)/usr/bin/vnproxctl

	install -m 0755 bin/vnprox-setup $(PKG_ROOT)/usr/bin/vnprox-setup
	install -m 0644 systemd/vnprox.service $(PKG_ROOT)/lib/systemd/system/vnprox.service
	# T-1901: the optional scheduled-backup timer. Installed but never
	# enabled by postinst — see the unit's own header and
	# docs/deployment.md's "Backup and disaster recovery" section.
	install -m 0644 systemd/vnprox-backup.service $(PKG_ROOT)/lib/systemd/system/vnprox-backup.service
	install -m 0644 systemd/vnprox-backup.timer $(PKG_ROOT)/lib/systemd/system/vnprox-backup.timer
	install -m 0640 config/vnprox.toml $(PKG_ROOT)/etc/vnprox/vnprox.toml  # T-2905: can carry dev_ticket_password — never world-readable

	# T-2106: licence and attribution. Debian policy requires a copyright
	# file at /usr/share/doc/<pkg>/copyright; LICENSE/NOTICE/THIRD-PARTY
	# ship alongside it because Apache-2.0 §4 requires the NOTICE to travel
	# with any redistribution, and the bundled SPA includes EPL-2.0 code
	# (elkjs) whose recipients must be able to find its licence and source.
	install -d -m 0755 $(PKG_ROOT)/usr/share/doc/vnprox
	install -m 0644 $(ROOT_DIR)/packaging/debian/copyright $(PKG_ROOT)/usr/share/doc/vnprox/copyright
	install -m 0644 $(ROOT_DIR)/LICENSE $(PKG_ROOT)/usr/share/doc/vnprox/LICENSE
	install -m 0644 $(ROOT_DIR)/NOTICE $(PKG_ROOT)/usr/share/doc/vnprox/NOTICE
	install -m 0644 $(ROOT_DIR)/THIRD-PARTY-LICENSES.md $(PKG_ROOT)/usr/share/doc/vnprox/THIRD-PARTY-LICENSES.md

	sed -e 's/@VERSION@/$(VERSION)/' -e 's/@ARCH@/$(ARCH)/' debian/control.tmpl > $(PKG_ROOT)/DEBIAN/control
	install -m 0644 debian/conffiles $(PKG_ROOT)/DEBIAN/conffiles
	install -m 0755 debian/postinst $(PKG_ROOT)/DEBIAN/postinst
	install -m 0755 debian/prerm $(PKG_ROOT)/DEBIAN/prerm
	install -m 0755 debian/postrm $(PKG_ROOT)/DEBIAN/postrm

	@echo ">> packaging: dpkg-deb --build (SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH))"
	# SOURCE_DATE_EPOCH is exported above; dpkg-deb reads it from the
	# environment (`man dpkg-deb`) — no separate flag needed.
	dpkg-deb --root-owner-group --build $(PKG_ROOT) $(DEB_FILE)
	@echo ">> packaging: built $(DEB_FILE)"
	@if command -v dpkg-deb >/dev/null 2>&1; then dpkg-deb --info $(DEB_FILE); fi

clean:
	rm -rf $(BUILD_DIR)
