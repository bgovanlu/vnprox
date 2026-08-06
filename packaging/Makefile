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

.PHONY: deb clean

deb:
	@echo ">> packaging: building $(PKG_NAME) $(VERSION) ($(ARCH))"
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
	cd $(ROOT_DIR) && GOARCH=$(ARCH) $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(PKG_ROOT)/usr/bin/vnproxd ./cmd/vnproxd
	cd $(ROOT_DIR) && GOARCH=$(ARCH) $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(PKG_ROOT)/usr/bin/vnproxctl ./cmd/vnproxctl
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
	install -m 0644 config/vnprox.toml $(PKG_ROOT)/etc/vnprox/vnprox.toml

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

	@echo ">> packaging: dpkg-deb --build"
	dpkg-deb --root-owner-group --build $(PKG_ROOT) $(DEB_FILE)
	@echo ">> packaging: built $(DEB_FILE)"
	@if command -v dpkg-deb >/dev/null 2>&1; then dpkg-deb --info $(DEB_FILE); fi

clean:
	rm -rf $(BUILD_DIR)
