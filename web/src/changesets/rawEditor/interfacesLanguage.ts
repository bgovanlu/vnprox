// SPDX-License-Identifier: Apache-2.0

// A Monarch grammar for /etc/network/interfaces(5) (ifupdown2 stanza
// syntax), for T-208's raw editor's syntax highlighting. Monarch tokenizers
// are plain declarative objects (regex -> token-name tables) — this module
// has no dependency on `monaco-editor` itself, so it (and its grammar
// rules) can be unit-tested directly; `registerInterfacesLanguage` is the
// one function that actually touches a monaco instance, and it takes that
// instance as a parameter (typed to the minimal subset it calls) rather
// than importing the real package, so a test can pass a fake and assert on
// what gets registered without loading Monaco at all.
//
// Grammar coverage mirrors internal/host/interfaces_parse.go's reserved
// keywords (auto, allow-*, no-auto-down, no-scripts, rename, source,
// source-directory, mapping, iface) plus the common option vocabulary
// (address families/methods, bridge-/bond-/ovs_ options, addresses).

export const INTERFACES_LANGUAGE_ID = "vnprox-interfaces";

/** The address families and methods interfaces(5)/ifupdown2 declares after
 * `iface <name> <family> <method>` — highlighted as a "type" token. */
const FAMILIES_AND_METHODS = ["inet", "inet6", "static", "dhcp", "dhcp6", "manual", "loopback"];

/** interfaces(5)'s reserved top-level stanza keywords other than `iface`
 * (which gets its own rule since it opens a multi-line stanza). */
const TOP_LEVEL_KEYWORDS = ["no-auto-down", "no-scripts", "rename", "source-directory", "source", "mapping"];

/** A representative (non-exhaustive) set of the option keys the T-102/T-204
 * corpus uses most, purely for a friendlier "keyword.option" color — any
 * other option name still tokenizes fine as a plain attribute name via the
 * generic option-line rule below, so this list is a highlighting nicety,
 * not a whitelist the parser enforces (the real interfaces(5) grammar
 * accepts arbitrary option keys inside a stanza). */
const COMMON_OPTIONS = [
  "address",
  "netmask",
  "gateway",
  "mtu",
  "bridge-ports",
  "bridge-vlan-aware",
  "bridge-vids",
  "bridge-stp",
  "bond-slaves",
  "bond-mode",
  "bond-miimon",
  "bond-lacp-rate",
  "bond-xmit-hash-policy",
  "vlan-raw-device",
  "vlan-id",
  "ovs_type",
  "ovs_ports",
  "ovs_bonds",
  "ovs_bridge",
];

/** Minimal shape of Monaco's Monarch tokenizer schema this grammar uses —
 * a subset of `monaco.languages.IMonarchLanguage`, kept local so this file
 * never needs to import `monaco-editor` (which is lazy-loaded, per AC4). */
export interface MonarchLanguage {
  defaultToken: string;
  ignoreCase: boolean;
  tokenizer: {
    root: [RegExp, string][];
  };
}

export const interfacesMonarchLanguage: MonarchLanguage = {
  defaultToken: "",
  ignoreCase: false,
  tokenizer: {
    root: [
      // Comments: interfaces(5) has no end-of-line comments, only whole
      // lines starting with '#' (matching host.KindComment's definition).
      [/^\s*#.*$/, "comment"],

      // The stanza-opening keyword line: `iface <name> <family> <method>
      // [inherits ...]`.
      [/^(\s*)(iface)(\s+)(\S+)(\s+)(\S+)(\s+)(\S+)/, "keyword"],
      [/^(\s*)(iface)\b/, "keyword"],

      // `auto <ifaces...>` / `allow-<class> <ifaces...>`.
      [/^(\s*)(auto|allow-[\w-]+)\b/, "keyword"],

      // Every other reserved top-level keyword.
      [new RegExp(`^(\\s*)(${TOP_LEVEL_KEYWORDS.join("|")})\\b`), "keyword"],

      // Address family / method tokens anywhere on a line (the iface
      // header line's 3rd/4th field, matched generically rather than by
      // position so `inet6 static` etc. all light up the same way).
      [new RegExp(`\\b(${FAMILIES_AND_METHODS.join("|")})\\b`), "type"],

      // A recognized option key at the start of an (indented) option line.
      [new RegExp(`^(\\s+)(${COMMON_OPTIONS.join("|")})\\b`), "keyword.option"],

      // Any other option line's leading key (unrecognized, but still an
      // option name per interfaces(5) — any non-blank, non-comment,
      // non-reserved-keyword line inside an open stanza).
      [/^(\s+)([\w.-]+)(?=\s|$)/, "attribute.name"],

      // CIDR / bare IPv4 addresses.
      [/\b\d{1,3}(\.\d{1,3}){3}(\/\d{1,2})?\b/, "number"],
      // Bare IPv6 addresses (loose match — good enough for highlighting).
      [/\b[0-9a-fA-F:]*:[0-9a-fA-F:]+(\/\d{1,3})?\b/, "number"],
    ],
  },
};

/** The subset of the real `monaco` namespace registerInterfacesLanguage
 * calls — small enough to satisfy with a test double, so this function is
 * unit-testable without importing the real (lazy-loaded) monaco-editor
 * package. */
export interface MonacoLanguagesLike {
  languages: {
    getLanguages(): { id: string }[];
    register(language: { id: string }): void;
    setMonarchTokensProvider(languageId: string, language: MonarchLanguage): void;
    setLanguageConfiguration(languageId: string, configuration: { comments: { lineComment: string } }): void;
  };
}

/** Registers the vnprox-interfaces language (id + Monarch tokenizer +
 * comment-toggle config) with monaco, once — idempotent across repeated
 * calls (e.g. every time the raw editor mounts) since
 * `monaco.languages.register` would otherwise accumulate duplicate
 * registrations for the same id across editor open/close cycles. */
export function registerInterfacesLanguage(monaco: MonacoLanguagesLike): void {
  const alreadyRegistered = monaco.languages.getLanguages().some((l) => l.id === INTERFACES_LANGUAGE_ID);
  if (!alreadyRegistered) {
    monaco.languages.register({ id: INTERFACES_LANGUAGE_ID });
  }
  monaco.languages.setMonarchTokensProvider(INTERFACES_LANGUAGE_ID, interfacesMonarchLanguage);
  monaco.languages.setLanguageConfiguration(INTERFACES_LANGUAGE_ID, { comments: { lineComment: "#" } });
}
