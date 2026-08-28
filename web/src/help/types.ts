// SPDX-License-Identifier: Apache-2.0

// The content model for vnprox's in-app help (planning/tasks/phase-22.md).
//
// Help content is typed TypeScript bundled into the SPA rather than
// markdown fetched from the daemon, for three reasons: it adds no API
// surface (the v3.0 platform freeze is additive-only and this needs
// nothing from it), it is available when the daemon is unreachable —
// which is precisely when someone needs the rollback and CLI-escape-hatch
// topics — and it lets the coverage gate in coverage.test.ts typecheck the
// inventory instead of parsing prose at runtime.

/** Which kind of thing a topic documents. Drives grouping in the browse
 * index and the coverage gate's route/anchor requirements. */
export type HelpSurface =
  /** A routed screen. Every route in App.tsx must have one. */
  | "page"
  /** A section or side panel inside a screen, reached via <HelpAnchor>. */
  | "panel"
  /** A modal, wizard, or drawer. */
  | "dialog"
  /** A cross-cutting idea the UI assumes you already hold. */
  | "concept"
  /** Lookup material: keys, exit codes, glossary-shaped things. */
  | "reference"
  /** A capability the daemon implements that has **no screen in this
   * build** — reachable from the API or `vnproxctl` only.
   *
   * T-3006 added this because the alternative was worse in both
   * directions. Four topics (`ipv6-planning`, `ipam-external-sync`,
   * `ipam-cross-cluster`, `scheduled-apply`) were written as `panel` and
   * described panels that have never existed in `web/src`; the daemon
   * serves every route behind them. Deleting the topics would hide a
   * shipped capability, and leaving them as `panel` sends an operator
   * hunting a screen that isn't there — the same "an absent thing
   * rendered as a present one" defect this repo keeps finding.
   *
   * So the absence is modelled instead of papered over: the browse index
   * groups these under their own heading, and coverage.test.ts requires
   * each one to name the route or CLI verb that does reach it. */
  | "headless";

export interface HelpSection {
  readonly heading: string;
  /** Plain text with two inline markers — `**bold**` and `` `code` ``.
   * See inline.ts; deliberately not markdown. */
  readonly body: string;
}

export interface HelpTopic {
  /** Stable kebab-case id. Referenced by ROUTE_HELP, <HelpAnchor topic>,
   * and `seeAlso`, so renaming one is a breaking change to those. */
  readonly id: string;
  readonly title: string;
  readonly surface: HelpSurface;
  /** One paragraph answering "what is this screen, and why would I be
   * here?". Shown under the title and used as the search-result snippet. */
  readonly summary: string;
  readonly sections: readonly HelpSection[];
  /** Concrete "how do I…" steps, rendered as an ordered list. */
  readonly steps?: readonly string[];
  /** Ids of related topics. Every one must resolve — enforced by the gate. */
  readonly seeAlso?: readonly string[];
  /** The repo doc this topic was written from. Help that invents behaviour
   * is worse than no help; this is how a reviewer checks it didn't. */
  readonly docRef: string;
  /** Extra search terms that don't appear in the prose (synonyms, the
   * name a Proxmox admin would reach for, the CLI verb). */
  readonly keywords?: readonly string[];
}
