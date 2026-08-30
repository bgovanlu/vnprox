// SPDX-License-Identifier: Apache-2.0

// A minimal, self-contained right-click context menu — positioned at the
// pointer rather than anchored to a trigger element, which is why this
// isn't built on the already-used @radix-ui/react-dropdown-menu (that
// primitive's positioning is Popper-anchored to its Trigger; a right-click
// menu has no trigger element, only a click point). Kept intentionally
// small rather than pulling in a new dependency (@radix-ui/react-context-menu)
// for one menu with a handful of items — see T-504's report for the note.
import { useEffect } from "react";

export interface ContextMenuItem {
  label: string;
  onSelect: () => void;
}

export interface ContextMenuProps {
  x: number;
  y: number;
  items: ContextMenuItem[];
  onClose: () => void;
}

export function ContextMenu({ x, y, items, onClose }: ContextMenuProps) {
  useEffect(() => {
    function handleOutside(): void {
      onClose();
    }
    function handleKey(e: KeyboardEvent): void {
      if (e.key === "Escape") onClose();
    }
    // Bubble phase (not capture): an in-menu click stops propagation before
    // it ever reaches these window listeners (see the item button's and
    // the menu container's own onClick/onContextMenu below) — only a
    // genuine outside click/right-click reaches here.
    //
    // Attaching is deferred to the next macrotask: the right-click that
    // *opens* this menu is still the current native "contextmenu" event's
    // dispatch when this effect first runs (React commits the state update
    // that renders this component before that dispatch has necessarily
    // finished bubbling to `window`); attaching synchronously let that same
    // event immediately close the menu it had just opened.
    const timer = setTimeout(() => {
      window.addEventListener("click", handleOutside);
      window.addEventListener("contextmenu", handleOutside);
    }, 0);
    window.addEventListener("keydown", handleKey);
    return () => {
      clearTimeout(timer);
      window.removeEventListener("click", handleOutside);
      window.removeEventListener("contextmenu", handleOutside);
      window.removeEventListener("keydown", handleKey);
    };
  }, [onClose]);

  if (items.length === 0) return null;

  return (
    <div
      role="menu"
      style={{ position: "fixed", left: x, top: y, zIndex: 60 }}
      className="min-w-[12rem] rounded-md border border-border bg-white p-1 shadow-lg dark:bg-slate-900"
      onClick={(e) => {
        e.stopPropagation();
      }}
      onContextMenu={(e) => {
        e.preventDefault();
        e.stopPropagation();
      }}
    >
      {items.map((item) => (
        <button
          key={item.label}
          type="button"
          role="menuitem"
          onClick={(e) => {
            e.stopPropagation();
            item.onSelect();
            onClose();
          }}
          className="block w-full rounded px-2.5 py-1.5 text-left text-sm hover:bg-slate-100 dark:hover:bg-slate-800"
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}
