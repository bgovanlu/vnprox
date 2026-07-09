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
	rm -rf $(PKG_ROOT)
	install -d -m 0755 $(PKG_ROOT)/DEBIAN
	install -d -m 0755 $(PKG_ROOT)/usr/bin
	install -d -m 0755 $(PKG_ROOT)/lib/systemd/system
	install -d -m 0755 $(PKG_ROOT)/etc/vnprox
	install -d -m 0755 $(DIST_DIR)

	@echo ">> packaging: building vnproxd and vnproxctl ($(ARCH))"
	cd $(ROOT_DIR) && GOARCH=$(ARCH) $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(PKG_ROOT)/usr/bin/vnproxd ./cmd/vnproxd
	cd $(ROOT_DIR) && GOARCH=$(ARCH) $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(PKG_ROOT)/usr/bin/vnproxctl ./cmd/vnproxctl

	install -m 0755 bin/vnprox-setup $(PKG_ROOT)/usr/bin/vnprox-setup
	install -m 0644 systemd/vnprox.service $(PKG_ROOT)/lib/systemd/system/vnprox.service
	install -m 0644 config/vnprox.toml $(PKG_ROOT)/etc/vnprox/vnprox.toml

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
