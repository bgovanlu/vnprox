// A small render-error boundary. React has no hook form of this, so it stays
// a class component. Wrap any subtree whose crash should degrade gracefully
// (a fallback) instead of unmounting the whole app to a blank screen — most
// importantly the wizard topology preview, which drives the real React Flow
// canvas and can fail on inputs unit tests (which mock the canvas) never
// exercise.
import { Component, type ErrorInfo, type ReactNode } from "react";

interface ErrorBoundaryProps {
  children: ReactNode;
  /** Rendered in place of the subtree once it has thrown. */
  fallback?: ReactNode;
  /** Tag included in the console error, to aid debugging which boundary fired. */
  label?: string;
}

interface ErrorBoundaryState {
  hasError: boolean;
}

export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { hasError: false };

  static getDerivedStateFromError(): ErrorBoundaryState {
    return { hasError: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // eslint-disable-next-line no-console -- surfacing the real cause is the point
    console.error(`ErrorBoundary${this.props.label ? ` [${this.props.label}]` : ""}:`, error, info.componentStack);
  }

  render(): ReactNode {
    if (this.state.hasError) {
      return this.props.fallback ?? null;
    }
    return this.props.children;
  }
}
