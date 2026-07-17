// A small render-error boundary. React has no hook form of this, so it stays
// a class component. Wrap any subtree whose crash should degrade gracefully
// (a fallback) instead of unmounting the whole app to a blank screen — most
// importantly the wizard topology preview, which drives the real React Flow
// canvas and can fail on inputs unit tests (which mock the canvas) never
// exercise.
import { Component, type ErrorInfo, type ReactNode } from "react";
import type { Density } from "./density";

interface ErrorBoundaryProps {
  children: ReactNode;
  /** Rendered in place of the subtree once it has thrown. When omitted,
   * falls back to this component's own built-in "Something went wrong"
   * panel (below) rather than silently unmounting to a blank area. */
  fallback?: ReactNode;
  /** Tag included in the console error, to aid debugging which boundary fired. */
  label?: string;
  /** T-905: compact/comfortable spacing (density.ts) for the *built-in*
   * default fallback panel only — a caller-supplied `fallback` owns its
   * own layout, so this has no effect once `fallback` is set. A class
   * component can't call the `useDensity()` hook (no ambient-provider
   * fallback the way the function components in this directory get), so
   * this stays an explicit prop rather than reading context — pass it
   * explicitly if the surrounding subtree is `<DensityProvider
   * density="compact">`. Defaults to "comfortable". */
  density?: Density;
}

interface ErrorBoundaryState {
  hasError: boolean;
}

const DENSITY_PADDING: Record<Density, string> = { comfortable: "p-6", compact: "p-3" };

export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { hasError: false };

  static getDerivedStateFromError(): ErrorBoundaryState {
    return { hasError: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // Pre-existing note: this repo's eslint config does not flag
    // console.error (only console.log), so no disable comment is needed
    // here — surfacing the real cause via the console is the point.
    console.error(`ErrorBoundary${this.props.label ? ` [${this.props.label}]` : ""}:`, error, info.componentStack);
  }

  render(): ReactNode {
    if (this.state.hasError) {
      if (this.props.fallback !== undefined) return this.props.fallback;
      const density = this.props.density ?? "comfortable";
      return (
        <div
          role="alert"
          data-density={density}
          className={`rounded-lg border border-red-300 bg-red-50 text-center text-red-800 dark:border-red-700 dark:bg-red-950 dark:text-red-200 ${DENSITY_PADDING[density]}`}
        >
          <p className="font-medium">This page hit an error{this.props.label ? ` (${this.props.label})` : ""}.</p>
          <p className="mt-1 text-sm text-red-700 dark:text-red-300">
            Reload the page — if it keeps happening, check the browser console for details.
          </p>
        </div>
      );
    }
    return this.props.children;
  }
}
