// SPDX-License-Identifier: Apache-2.0

// The rendered half of the topology a11y bridge (T-901): a parallel DOM
// layer of one focusable, correctly-labeled proxy element per visible canvas
// entity, kept positioned over its on-canvas box and navigable by keyboard
// with roving tabindex. This is the seam T-905 (WCAG keyboard nav + SR
// labels) and T-903 (palette-/arrow-driven map navigation) consume — they
// drive it via the same `activeId`/`onActiveChange`/`onActivate` props, they
// do not reimplement it.
//
// Performance: the proxies are laid out in GRAPH space (the same coordinates
// the canvas draws at); pan/zoom is applied once as a single CSS transform on
// the container, so a pan frame updates one element's transform, not N
// buttons. The button list is memoized on the proxy set + focus state, so an
// ordinary pan/zoom re-render (viewport only) does not re-diff every proxy.
//
// Roving focus (WAI-ARIA "roving tabindex" pattern): exactly one proxy is in
// the tab order (tabIndex 0) at a time — the active one; the rest are
// tabIndex -1 and reachable only via arrow keys. Enter/Space activates
// (selects) the entity the way a click would. Proxies are visually
// transparent (the canvas draws the real pixels) but carry a visible focus
// ring so a sighted keyboard user sees where they are.
import { useCallback, useEffect, useMemo, useRef } from "react";
import type { Viewport } from "./canvasScene";
import type { A11yProxy } from "./a11yBridge";
import { nextRovingId, rovingOrder } from "./a11yBridge";

export interface TopologyA11yLayerProps {
  proxies: A11yProxy[];
  /** Current pan/zoom, applied as one container transform. */
  viewport: Viewport;
  /** The entity currently holding roving focus (controlled by the parent so
   * T-903's palette can move it programmatically). undefined = the first in
   * reading order is the tab stop, but nothing is focused yet. */
  activeId: string | undefined;
  /** Roving focus moved to this entity (arrow key, or a proxy received DOM
   * focus via Tab/click). */
  onActiveChange: (id: string) => void;
  /** Enter/Space (or a direct click on a proxy) — activate = the same thing a
   * canvas click does (select / expand a guest-group pill). */
  onActivate: (id: string) => void;
  /** Accessible name for the whole map region. */
  regionLabel?: string;
}

export function TopologyA11yLayer({
  proxies,
  viewport,
  activeId,
  onActiveChange,
  onActivate,
  regionLabel = "Topology map",
}: TopologyA11yLayerProps) {
  const order = useMemo(() => rovingOrder(proxies), [proxies]);
  const buttonRefs = useRef(new Map<string, HTMLButtonElement>());
  // Only pull DOM focus to the active proxy when the move originated from
  // keyboard navigation inside this layer — never steal focus on an ordinary
  // pan/zoom re-render (which changes only the container transform).
  const pendingFocus = useRef<string | undefined>(undefined);

  // The effective tab stop: the explicitly-active entity, else the first in
  // reading order so the region is always keyboard-enterable via Tab.
  const tabStopId = activeId ?? order[0]?.id;

  useEffect(() => {
    const id = pendingFocus.current;
    if (id === undefined) return;
    pendingFocus.current = undefined;
    buttonRefs.current.get(id)?.focus();
  }, [activeId]);

  const move = useCallback(
    (delta: 1 | -1) => {
      const next = nextRovingId(order, activeId ?? order[0]?.id, delta);
      if (next === undefined) return;
      pendingFocus.current = next;
      onActiveChange(next);
    },
    [order, activeId, onActiveChange],
  );

  const handleKeyDown = useCallback(
    (evt: React.KeyboardEvent<HTMLDivElement>) => {
      switch (evt.key) {
        case "ArrowRight":
        case "ArrowDown":
          evt.preventDefault();
          move(1);
          break;
        case "ArrowLeft":
        case "ArrowUp":
          evt.preventDefault();
          move(-1);
          break;
        case "Home": {
          const first = order[0]?.id;
          if (first !== undefined) {
            evt.preventDefault();
            pendingFocus.current = first;
            onActiveChange(first);
          }
          break;
        }
        case "End": {
          const last = order[order.length - 1]?.id;
          if (last !== undefined) {
            evt.preventDefault();
            pendingFocus.current = last;
            onActiveChange(last);
          }
          break;
        }
        case "Enter":
        case " ": {
          const target = activeId ?? tabStopId;
          if (target !== undefined) {
            evt.preventDefault();
            onActivate(target);
          }
          break;
        }
        default:
          break;
      }
    },
    [move, order, activeId, tabStopId, onActiveChange, onActivate],
  );

  // Memoized button list: recreated only when the proxy set or the current
  // tab stop changes — NOT on every pan/zoom (which only touches the
  // container transform below).
  const buttons = useMemo(
    () =>
      proxies.map((p) => (
        <button
          key={p.id}
          type="button"
          ref={(el) => {
            if (el) buttonRefs.current.set(p.id, el);
            else buttonRefs.current.delete(p.id);
          }}
          data-entity-id={p.id}
          aria-label={p.ariaLabel}
          aria-pressed={p.selected}
          tabIndex={p.id === tabStopId ? 0 : -1}
          onFocus={() => {
            if (p.id !== activeId) onActiveChange(p.id);
          }}
          onClick={() => {
            onActivate(p.id);
          }}
          // pointer-events stay off (inherited from the container): the
          // canvas beneath owns ALL mouse interaction (pan/drag/hover/click
          // via hit-testing), so these proxies never intercept a click meant
          // for the canvas. They remain Tab-focusable and Enter/Space-
          // activatable (keyboard/AT don't go through pointer hit-testing),
          // which is exactly the keyboard surface T-905/T-903 need.
          className="absolute m-0 cursor-default border-0 bg-transparent p-0 focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
          style={{
            left: `${String(p.graph.x)}px`,
            top: `${String(p.graph.y)}px`,
            width: `${String(p.graph.width)}px`,
            height: `${String(p.graph.height)}px`,
          }}
        />
      )),
    [proxies, tabStopId, activeId, onActiveChange, onActivate],
  );

  return (
    <div
      // role="application": this is a custom widget with its own arrow-key
      // navigation model, not a document region — so AT passes the arrow keys
      // through to us rather than using them for browse-mode caret movement.
      // T-905 layers axe checks and refinements on top of this.
      role="application"
      aria-label={`${regionLabel}, ${String(proxies.length)} entities`}
      className="pointer-events-none absolute inset-0 overflow-hidden"
      onKeyDown={handleKeyDown}
    >
      <div
        className="pointer-events-none absolute left-0 top-0"
        style={{
          transform: `translate(${String(viewport.x)}px, ${String(viewport.y)}px) scale(${String(viewport.zoom)})`,
          transformOrigin: "0 0",
        }}
      >
        {buttons}
      </div>
    </div>
  );
}
