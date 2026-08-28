// SPDX-License-Identifier: Apache-2.0

// T-903: wires rovingFocus.ts's pure ordering logic to real DOM entities.
// Every clickable topology entity — EntityNode.tsx (Graph view) and
// SwitchFaceplate.tsx/SwitchView.tsx's uplink/port/VLAN/VNet buttons
// (Switch view) — carries a `data-entity-ref` attribute for exactly this;
// reading live `getBoundingClientRect()` geometry rather than any stored
// x/y means this hook works identically against whichever DOM renderer is
// currently mounted (v1 today; T-901/T-905's canvas a11y bridge once that
// lands — no rework needed here, per the task card) without either view
// threading coordinates through props.
import { useEffect, useRef } from "react";
import { keyToDirection, nextFocusId, type PositionedEntity } from "./rovingFocus";

const ENTITY_SELECTOR = "[data-entity-ref]";

export interface UseRovingFocusOptions {
  /** The scrollable region containing every roving-focusable entity (the
   * Topology page's canvas/switch-view wrapper). A ref rather than a
   * direct element so it can be attached before the view underneath ever
   * mounts. */
  containerRef: React.RefObject<HTMLElement | null>;
  /** Called when Enter activates the focused entity — the same handler a
   * click on that entity invokes (TopologyPage's handleNodeClick: selection
   * → inspector open), so keyboard activation is never a second, divergent
   * code path. */
  onActivate: (id: string) => void;
}

function collectEntities(container: HTMLElement): PositionedEntity[] {
  return Array.from(container.querySelectorAll<HTMLElement>(ENTITY_SELECTOR))
    .filter((el) => el.dataset.entityRef !== undefined)
    .map((el) => {
      const rect = el.getBoundingClientRect();
      return { id: el.dataset.entityRef ?? "", x: rect.left, y: rect.top };
    });
}

function focusEntity(container: HTMLElement, id: string): void {
  const target = Array.from(container.querySelectorAll<HTMLElement>(ENTITY_SELECTOR)).find(
    (el) => el.dataset.entityRef === id,
  );
  target?.focus();
}

/**
 * Arrow-key roving focus across every `data-entity-ref` element inside
 * `containerRef`, in visual-adjacency order (rovingFocus.ts); Enter
 * activates the focused entity via `onActivate`. Attaches one delegated
 * keydown listener on the container rather than one per entity, so it
 * keeps working as entities mount/unmount (guest-group expansion, WS
 * deltas, layer toggles) without re-wiring.
 */
export function useRovingFocus({ containerRef, onActivate }: UseRovingFocusOptions): void {
  // Kept in a ref, like useKeyboardShortcuts.ts's optionsRef, so the effect
  // below doesn't need to re-attach its listener whenever the caller passes
  // a freshly-created `onActivate` closure (TopologyPage's handleNodeClick
  // is a plain function redefined every render, not a memoized callback).
  const onActivateRef = useRef(onActivate);
  onActivateRef.current = onActivate;

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    function handleKeyDown(event: KeyboardEvent): void {
      const target = event.target;
      if (!(target instanceof HTMLElement)) return;
      const currentRef = target.dataset.entityRef;
      if (currentRef === undefined) return;
      const currentContainer = containerRef.current;
      if (!currentContainer) return;

      const direction = keyToDirection(event.key);
      if (direction) {
        event.preventDefault();
        const next = nextFocusId(collectEntities(currentContainer), currentRef, direction);
        if (next) focusEntity(currentContainer, next);
        return;
      }

      if (event.key === "Enter") {
        // Prevent the browser's own default activation for this key (a
        // focused native <button> — Switch view's ports — would otherwise
        // fire its own onClick too, double-invoking the same handler,
        // e.g. double-toggling a collapsed guest-group pill back closed).
        event.preventDefault();
        onActivateRef.current(currentRef);
      }
    }

    container.addEventListener("keydown", handleKeyDown);
    return () => {
      container.removeEventListener("keydown", handleKeyDown);
    };
  }, [containerRef]);
}
