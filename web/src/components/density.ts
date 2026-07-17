// T-905 component-contract pass: the one density-mode seam every component
// in this directory (Button, Dialog, Drawer, EmptyState, ErrorBoundary,
// Table, Toast, Tooltip) reads instead of each hand-rolling its own
// compact/comfortable padding scale. Two ways to set it:
//
//   1. Per-instance: pass `density="compact"` directly to a component.
//   2. Ambient: wrap a subtree in `<DensityProvider density="compact">` (a
//      dense table page, a data-heavy drawer) so every density-aware
//      component underneath defaults to it without threading the prop
//      through every call site.
//
// An explicit per-instance prop always wins over the ambient provider — see
// `useDensity`. Comfortable is the app-wide default or (no provider
// mounted), matching every one of these components' current spacing scale,
// so introducing this seam is additive: no existing call site's rendered
// output changes.
import { createContext, useContext, type ReactNode, createElement } from "react";

export type Density = "compact" | "comfortable";

const DensityContext = createContext<Density>("comfortable");

export function DensityProvider({ density, children }: { density: Density; children: ReactNode }) {
  return createElement(DensityContext.Provider, { value: density }, children);
}

/** Resolves the effective density for a component instance: the explicit
 * `density` prop if the caller passed one, else whatever `<DensityProvider>`
 * is in scope (or "comfortable" absent one). Every density-aware component
 * calls this exactly once with its own `density?: Density` prop. */
export function useDensity(explicit?: Density): Density {
  const ambient = useContext(DensityContext);
  return explicit ?? ambient;
}
