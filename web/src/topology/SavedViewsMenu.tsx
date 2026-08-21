// T-907's saved-views toolbar control: save the current topology page
// state as a named view, load/delete a previously-saved one, or copy a
// shareable link that carries the same state in the URL (docs/api.md's
// Saved views & annotations section — "state lives in the URL, not only
// server-side").
import * as RadixDropdown from "@radix-ui/react-dropdown-menu";
import { Button } from "../components/Button";
import { useToast } from "../components/Toast";
import { encodeViewToSearch, type SavedViewState } from "./savedViews";
import { loadSavedView, useDeleteViewMutation, useSavedViewsQuery, useSaveViewMutation } from "./savedViewsQueries";

export interface SavedViewsMenuProps {
  /** Reads the current, capturable page state (docs/api.md's saved-view
   * shape: layers, vlanFilter, zoom, viewport, selection, view) at the
   * moment it's needed — a getter, not a static prop, since the v1/v2
   * renderers track pan/zoom differently (a v1 React Flow instance's live
   * viewport vs. v2's own ref-tracked one — see TopologyPage's
   * getCurrentViewState) and a plain prop value would go stale between
   * TopologyPage renders. */
  getCurrentState: () => SavedViewState;
  /** Applies a loaded/decoded view to the live page. */
  onLoad: (state: SavedViewState) => void;
}

const menuItemClass =
  "flex cursor-pointer items-center justify-between gap-2 rounded px-2 py-1.5 text-sm outline-none hover:bg-slate-100 dark:hover:bg-slate-800";

export function SavedViewsMenu({ getCurrentState, onLoad }: SavedViewsMenuProps) {
  const { data: views } = useSavedViewsQuery();
  const saveMutation = useSaveViewMutation();
  const deleteMutation = useDeleteViewMutation();
  const { toast } = useToast();

  function handleSave(): void {
    // window.prompt mirrors InspectorPanel's window.confirm convention for
    // simple, rare, blocking user input in this codebase — no dedicated
    // naming dialog component exists yet for this one-field case.
    const name = window.prompt("Name this view:");
    const trimmed = name?.trim();
    if (!trimmed) return;
    saveMutation.mutate(
      { name: trimmed, state: getCurrentState() },
      {
        onSuccess: () => {
          toast({ title: "View saved", description: `"${trimmed}" is ready to load anytime.` });
        },
        onError: () => {
          toast({ title: "Could not save view", variant: "error" });
        },
      },
    );
  }

  function handleLoad(name: string): void {
    void loadSavedView(name)
      .then((state) => {
        if (!state) {
          toast({ title: "Could not load view", description: "That saved view looks corrupted.", variant: "error" });
          return;
        }
        onLoad(state);
      })
      .catch(() => {
        toast({ title: "Could not load view", variant: "error" });
      });
  }

  function handleDelete(name: string): void {
    deleteMutation.mutate(name, {
      onError: () => {
        toast({ title: "Could not delete view", variant: "error" });
      },
    });
  }

  function handleCopyLink(): void {
    const params = encodeViewToSearch(getCurrentState());
    const url = `${window.location.origin}${window.location.pathname}?${params.toString()}`;
    void navigator.clipboard
      .writeText(url)
      .then(() => {
        toast({ title: "Link copied", description: "Opening it restores this exact view, even for someone with no saved views of their own." });
      })
      .catch(() => {
        toast({ title: "Could not copy link", variant: "error" });
      });
  }

  return (
    <RadixDropdown.Root>
      <RadixDropdown.Trigger asChild>
        <Button size="sm" variant="secondary">
          Views ▾
        </Button>
      </RadixDropdown.Trigger>
      <RadixDropdown.Portal>
        <RadixDropdown.Content
          align="end"
          sideOffset={6}
          className="z-50 min-w-[16rem] rounded-md border border-slate-200 bg-white p-1 shadow-lg dark:border-slate-700 dark:bg-slate-900"
        >
          <RadixDropdown.Item className={menuItemClass} onSelect={handleSave}>
            Save current view…
          </RadixDropdown.Item>
          <RadixDropdown.Item className={menuItemClass} onSelect={handleCopyLink}>
            Copy share link
          </RadixDropdown.Item>
          {views && views.length > 0 && (
            <>
              <RadixDropdown.Separator className="my-1 h-px bg-slate-200 dark:bg-slate-700" />
              {views.map((v) => (
                <RadixDropdown.Item
                  key={v.name}
                  className={menuItemClass}
                  onSelect={(e) => {
                    // Prevent the menu-item's own default select (which
                    // would close the menu before the delete button's own
                    // click has a chance to run) only when the delete
                    // affordance itself was the target.
                    if (e.target instanceof HTMLElement && e.target.closest('[data-role="delete-view"]')) {
                      e.preventDefault();
                      return;
                    }
                    handleLoad(v.name);
                  }}
                >
                  <span className="truncate">{v.name}</span>
                  <button
                    type="button"
                    data-role="delete-view"
                    aria-label={`Delete saved view: ${v.name}`}
                    className="shrink-0 text-slate-600 dark:text-slate-400 hover:text-red-600 dark:hover:text-red-400"
                    onClick={(e) => {
                      e.stopPropagation();
                      handleDelete(v.name);
                    }}
                  >
                    ✕
                  </button>
                </RadixDropdown.Item>
              ))}
            </>
          )}
          {(!views || views.length === 0) && (
            <p className="px-2 py-1.5 text-xs text-slate-600 dark:text-slate-400">No saved views yet.</p>
          )}
        </RadixDropdown.Content>
      </RadixDropdown.Portal>
    </RadixDropdown.Root>
  );
}
