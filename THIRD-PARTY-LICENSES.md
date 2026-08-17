# Third-party licenses

vnprox is distributed under the Apache License 2.0 (see `LICENSE` and `NOTICE`).
The artifacts it ships — the `vnproxd` binary and the embedded single-page
application — bundle third-party open-source components, listed here with the
licence each is distributed under.

**This file is generated.** Run `make third-party` to regenerate it after a
dependency change; a hand-edited copy will be overwritten. The generator is
`packaging/bin/gen-third-party-licenses.sh`.

## Components requiring particular attention

| Component | Licence | Why it is called out |
|---|---|---|
| `elkjs` | EPL-2.0 | Weak (file-level) copyleft, and it **does** ship inside the SPA bundle — it is the graph layout engine. Using it imposes no obligation on vnprox's own source, but recipients must be able to obtain the EPL text and elkjs's own source. Both are linked below. |
| `dompurify` | MPL-2.0 OR Apache-2.0 | Dual-licensed; vnprox elects **Apache-2.0**, matching this project's own licence. |
| `monaco-editor` | MIT | Ships as its own lazily-loaded chunk rather than in the main bundle. |
| Proxmox VE | AGPL-3.0 | **Not bundled and not linked.** vnprox interoperates with Proxmox VE only over its published HTTP API and on-disk configuration files, so no AGPL obligation attaches. See `NOTICE`. |

Full licence texts: [Apache-2.0](https://www.apache.org/licenses/LICENSE-2.0) ·
[MIT](https://opensource.org/license/mit) · [ISC](https://opensource.org/license/isc-license-txt) ·
[BSD-3-Clause](https://opensource.org/license/bsd-3-clause) ·
[BSD-2-Clause](https://opensource.org/license/bsd-2-clause) ·
[0BSD](https://opensource.org/license/0bsd) ·
[MPL-2.0](https://www.mozilla.org/en-US/MPL/2.0/) ·
[EPL-2.0](https://www.eclipse.org/legal/epl-2.0/) — elkjs source:
<https://github.com/kieler/elkjs>

## Go modules (`vnproxd`, `vnproxctl`)

Modules in the build graph, from `go list -m all`. Includes
test-only and transitive modules; not all are linked into the shipped
binaries.

| Module | Version |
|---|---|
| `github.com/alecthomas/kingpin/v2` | v2.4.0 |
| `github.com/alecthomas/units` | v0.0.0-20240927000941-0f3dac36c52b |
| `github.com/beorn7/perks` | v1.0.1 |
| `github.com/BurntSushi/toml` | v1.6.0 |
| `github.com/cespare/xxhash/v2` | v2.3.0 |
| `github.com/creack/pty` | v1.1.9 |
| `github.com/davecgh/go-spew` | v1.1.1 |
| `github.com/dustin/go-humanize` | v1.0.1 |
| `github.com/go-chi/chi/v5` | v5.3.1 |
| `github.com/golang-jwt/jwt/v5` | v5.3.1 |
| `github.com/golang/protobuf` | v1.5.0 |
| `github.com/google/go-cmp` | v0.7.0 |
| `github.com/google/pprof` | v0.0.0-20250317173921-a4b03ec1a45e |
| `github.com/google/uuid` | v1.6.0 |
| `github.com/hashicorp/golang-lru/v2` | v2.0.7 |
| `github.com/jpillora/backoff` | v1.0.0 |
| `github.com/julienschmidt/httprouter` | v1.3.0 |
| `github.com/klauspost/compress` | v1.19.0 |
| `github.com/kr/pretty` | v0.1.0 |
| `github.com/kr/text` | v0.2.0 |
| `github.com/mattn/go-isatty` | v0.0.20 |
| `github.com/munnerz/goautoneg` | v0.0.0-20191010083416-a7dc8b61c822 |
| `github.com/mwitkow/go-conntrack` | v0.0.0-20190716064945-2f068394615f |
| `github.com/ncruces/go-strftime` | v1.0.0 |
| `github.com/oklog/ulid/v2` | v2.1.1 |
| `github.com/pborman/getopt` | v0.0.0-20170112200414-7148bc3a4c30 |
| `github.com/pmezard/go-difflib` | v1.0.0 |
| `github.com/prometheus/client_golang` | v1.23.2 |
| `github.com/prometheus/client_model` | v0.6.2 |
| `github.com/prometheus/common` | v0.70.0 |
| `github.com/prometheus/procfs` | v0.21.0 |
| `github.com/remyoudompheng/bigfft` | v0.0.0-20230129092748-24d4a6f8daec |
| `github.com/stretchr/testify` | v1.11.1 |
| `github.com/vishvananda/netlink` | v1.3.1 |
| `github.com/vishvananda/netns` | v0.0.5 |
| `github.com/xhit/go-str2duration/v2` | v2.1.0 |
| `golang.org/x/mod` | v0.36.0 |
| `golang.org/x/net` | v0.56.0 |
| `golang.org/x/oauth2` | v0.36.0 |
| `golang.org/x/sync` | v0.20.0 |
| `golang.org/x/sys` | v0.46.0 |
| `golang.org/x/text` | v0.38.0 |
| `golang.org/x/tools` | v0.45.0 |
| `google.golang.org/protobuf` | v1.36.11 |
| `gopkg.in/check.v1` | v1.0.0-20180628173108-788fd7840127 |
| `gopkg.in/yaml.v3` | v3.0.1 |
| `go.uber.org/goleak` | v1.3.0 |
| `go.yaml.in/yaml/v2` | v2.4.4 |
| `modernc.org/ccgo/v4` | v4.34.4 |
| `modernc.org/cc/v4` | v4.28.4 |
| `modernc.org/fileutil` | v1.4.0 |
| `modernc.org/gc/v2` | v2.6.5 |
| `modernc.org/gc/v3` | v3.1.3 |
| `modernc.org/goabi0` | v0.2.0 |
| `modernc.org/libc` | v1.73.4 |
| `modernc.org/mathutil` | v1.7.1 |
| `modernc.org/memory` | v1.11.0 |
| `modernc.org/opt` | v0.2.0 |
| `modernc.org/sortutil` | v1.2.1 |
| `modernc.org/sqlite` | v1.53.0 |
| `modernc.org/strutil` | v1.2.1 |
| `modernc.org/token` | v1.1.0 |
| `nhooyr.io/websocket` | v1.8.17 |

## npm packages bundled into the SPA

Production dependency tree only (`license-checker --production`).
Build- and test-only tooling — Vite, Vitest, Playwright, ESLint,
TypeScript, axe-core, lightningcss — is **not** redistributed and is
therefore excluded.

| Package | Version | Licence |
|---|---|---|
| `@babel/runtime` | 7.29.7 | MIT |
| `@floating-ui/core` | 1.7.5 | MIT |
| `@floating-ui/dom` | 1.7.6 | MIT |
| `@floating-ui/react-dom` | 2.1.8 | MIT |
| `@floating-ui/utils` | 0.2.11 | MIT |
| `@radix-ui/primitive` | 1.1.5 | MIT |
| `@radix-ui/react-arrow` | 1.1.11 | MIT |
| `@radix-ui/react-collection` | 1.1.12 | MIT |
| `@radix-ui/react-compose-refs` | 1.1.3 | MIT |
| `@radix-ui/react-context` | 1.2.0 | MIT |
| `@radix-ui/react-dialog` | 1.1.19 | MIT |
| `@radix-ui/react-direction` | 1.1.2 | MIT |
| `@radix-ui/react-dismissable-layer` | 1.1.15 | MIT |
| `@radix-ui/react-dropdown-menu` | 2.1.20 | MIT |
| `@radix-ui/react-focus-guards` | 1.1.4 | MIT |
| `@radix-ui/react-focus-scope` | 1.1.12 | MIT |
| `@radix-ui/react-id` | 1.1.2 | MIT |
| `@radix-ui/react-menu` | 2.1.20 | MIT |
| `@radix-ui/react-popper` | 1.3.3 | MIT |
| `@radix-ui/react-portal` | 1.1.13 | MIT |
| `@radix-ui/react-presence` | 1.1.7 | MIT |
| `@radix-ui/react-primitive` | 2.1.7 | MIT |
| `@radix-ui/react-roving-focus` | 1.1.15 | MIT |
| `@radix-ui/react-slot` | 1.3.0 | MIT |
| `@radix-ui/react-tabs` | 1.1.17 | MIT |
| `@radix-ui/react-toast` | 1.2.19 | MIT |
| `@radix-ui/react-tooltip` | 1.2.12 | MIT |
| `@radix-ui/react-use-callback-ref` | 1.1.2 | MIT |
| `@radix-ui/react-use-controllable-state` | 1.2.3 | MIT |
| `@radix-ui/react-use-effect-event` | 0.0.3 | MIT |
| `@radix-ui/react-use-is-hydrated` | 0.1.1 | MIT |
| `@radix-ui/react-use-layout-effect` | 1.1.2 | MIT |
| `@radix-ui/react-use-rect` | 1.1.2 | MIT |
| `@radix-ui/react-use-size` | 1.1.2 | MIT |
| `@radix-ui/react-visually-hidden` | 1.2.7 | MIT |
| `@radix-ui/rect` | 1.1.2 | MIT |
| `@reduxjs/toolkit` | 2.12.0 | MIT |
| `@standard-schema/spec` | 1.1.0 | MIT |
| `@standard-schema/utils` | 0.3.0 | MIT |
| `@tanstack/query-core` | 5.101.2 | MIT |
| `@tanstack/react-query` | 5.101.2 | MIT |
| `@types/d3-array` | 3.2.2 | MIT |
| `@types/d3-color` | 3.1.3 | MIT |
| `@types/d3-drag` | 3.0.7 | MIT |
| `@types/d3-ease` | 3.0.2 | MIT |
| `@types/d3-interpolate` | 3.0.4 | MIT |
| `@types/d3-path` | 3.1.1 | MIT |
| `@types/d3-scale` | 4.0.9 | MIT |
| `@types/d3-selection` | 3.0.11 | MIT |
| `@types/d3-shape` | 3.1.8 | MIT |
| `@types/d3-time` | 3.0.4 | MIT |
| `@types/d3-timer` | 3.0.2 | MIT |
| `@types/d3-transition` | 3.0.9 | MIT |
| `@types/d3-zoom` | 3.0.8 | MIT |
| `@types/prop-types` | 15.7.15 | MIT |
| `@types/react` | 18.3.31 | MIT |
| `@types/react-dom` | 18.3.7 | MIT |
| `@types/trusted-types` | 2.0.7 | MIT |
| `@types/use-sync-external-store` | 0.0.6 | MIT |
| `@xyflow/react` | 12.11.2 | MIT |
| `@xyflow/system` | 0.0.79 | MIT |
| `aria-hidden` | 1.2.6 | MIT |
| `classcat` | 5.0.5 | MIT |
| `clsx` | 2.1.1 | MIT |
| `cookie` | 1.1.1 | MIT |
| `csstype` | 3.2.3 | MIT |
| `d3-array` | 3.2.4 | ISC |
| `d3-color` | 3.1.0 | ISC |
| `d3-dispatch` | 3.0.1 | ISC |
| `d3-drag` | 3.0.0 | ISC |
| `d3-ease` | 3.0.1 | BSD-3-Clause |
| `d3-format` | 3.1.2 | ISC |
| `d3-interpolate` | 3.0.1 | ISC |
| `d3-path` | 3.1.0 | ISC |
| `d3-scale` | 4.0.2 | ISC |
| `d3-selection` | 3.0.0 | ISC |
| `d3-shape` | 3.2.0 | ISC |
| `d3-time` | 3.1.0 | ISC |
| `d3-time-format` | 4.1.0 | ISC |
| `d3-timer` | 3.0.1 | ISC |
| `d3-transition` | 3.0.1 | ISC |
| `d3-zoom` | 3.0.0 | ISC |
| `decimal.js-light` | 2.5.1 | MIT |
| `detect-node-es` | 1.1.0 | MIT |
| `dompurify` | 3.4.11 | (MPL-2.0 OR Apache-2.0) |
| `elkjs` | 0.11.1 | EPL-2.0 |
| `es-toolkit` | 1.49.0 | MIT |
| `eventemitter3` | 5.0.4 | MIT |
| `get-nonce` | 1.0.1 | MIT |
| `html-parse-stringify` | 4.0.1 | MIT |
| `i18next` | 26.3.6 | MIT |
| `immer` | 11.1.11 | MIT |
| `internmap` | 2.0.3 | ISC |
| `js-tokens` | 4.0.0 | MIT |
| `loose-envify` | 1.4.0 | MIT |
| `marked` | 14.0.0 | MIT |
| `monaco-editor` | 0.55.1 | MIT |
| `react` | 18.3.1 | MIT |
| `react-dom` | 18.3.1 | MIT |
| `react-i18next` | 17.0.11 | MIT |
| `react-is` | 19.2.7 | MIT |
| `react-redux` | 9.3.0 | MIT |
| `react-remove-scroll` | 2.7.2 | MIT |
| `react-remove-scroll-bar` | 2.3.8 | MIT |
| `react-router` | 7.18.1 | MIT |
| `react-router-dom` | 7.18.1 | MIT |
| `react-style-singleton` | 2.2.3 | MIT |
| `recharts` | 3.9.2 | MIT |
| `redux` | 5.0.1 | MIT |
| `redux-thunk` | 3.1.0 | MIT |
| `reselect` | 5.2.0 | MIT |
| `scheduler` | 0.23.2 | MIT |
| `set-cookie-parser` | 2.7.2 | MIT |
| `tiny-invariant` | 1.3.3 | MIT |
| `tslib` | 2.8.1 | 0BSD |
| `use-callback-ref` | 1.3.3 | MIT |
| `use-sidecar` | 1.1.3 | MIT |
| `use-sync-external-store` | 1.6.0 | MIT |
| `victory-vendor` | 37.3.6 | MIT AND ISC |
| `zustand` | 4.5.7 | MIT |
| `zustand` | 5.0.14 | MIT |

---

Generated by `packaging/bin/gen-third-party-licenses.sh`.
