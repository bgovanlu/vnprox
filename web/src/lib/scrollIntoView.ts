// SPDX-License-Identifier: Apache-2.0

// jsdom (this app's test environment) does not implement
// Element.prototype.scrollIntoView at all, unlike every real browser
// (lib.dom.d.ts types it as always present, which is why a plain
// `el.scrollIntoView(...)` call — or even `el.scrollIntoView?.(...)` —
// both type-check fine but the latter still trips
// @typescript-eslint/no-unnecessary-condition since the type says it's
// never nullish). A runtime `typeof` guard is honest about the actual gap
// without a lint suppression.
export function scrollIntoViewIfSupported(el: Element | null | undefined, options?: ScrollIntoViewOptions): void {
  if (el && typeof el.scrollIntoView === "function") {
    el.scrollIntoView(options);
  }
}
