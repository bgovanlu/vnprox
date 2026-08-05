// Help panel state. A store rather than React context because <HelpAnchor>
// is placed deep inside feature modules that have no reason to know a help
// provider exists above them — the same reasoning the topology store and
// the changeset drawer already use in this codebase.
import { create } from "zustand";

// The control that opened the panel, so focus can go back to it on close.
//
// Radix's Dialog restores focus to its own <DialogTrigger>, and this panel
// has none — it is opened programmatically from the top bar, from F1, and
// from <HelpAnchor>s scattered through the feature modules. Without this,
// closing help drops focus to <body> and a keyboard user loses their place
// in the page they were reading about.
//
// Held as a module value rather than in the store's state because it is a
// live DOM node: nothing should re-render when it changes, and it must be
// captured synchronously inside the click handler, before React re-renders
// and Radix moves focus into the drawer.
let opener: HTMLElement | null = null;

function captureOpener(): void {
  opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
}

/** The element to return focus to when the panel closes, if it's still in
 * the document — a control on a page that has since unmounted is not a
 * focus target worth chasing. */
export function helpOpenerElement(): HTMLElement | null {
  return opener?.isConnected === true ? opener : null;
}

interface HelpState {
  open: boolean;
  /** The topic currently shown. Null only before the panel has ever been
   * opened. */
  topicId: string | null;
  /** Topics visited before the current one, most recent last. Lets
   * `seeAlso` navigation be reversible without a router. */
  history: string[];
  /** Free-text query in the panel's search box. Held here (not in the
   * component) so opening help from a search result and coming back keeps
   * the query. */
  query: string;

  openHelp: (topicId: string) => void;
  /** Navigate within the open panel, pushing the current topic onto the
   * back stack. */
  goToTopic: (topicId: string) => void;
  /** Show the browse index, pushing the current topic onto the back stack
   * so Back returns to what you were reading. */
  browseIndex: () => void;
  back: () => void;
  close: () => void;
  setQuery: (query: string) => void;
}

export const useHelpStore = create<HelpState>()((set) => ({
  open: false,
  topicId: null,
  history: [],
  query: "",

  openHelp: (topicId) => {
    captureOpener();
    // Opening from outside the panel is a fresh entry point, so the back
    // stack starts empty — "back" should never walk you into the topic you
    // were reading on a different page an hour ago.
    set({ open: true, topicId, history: [], query: "" });
  },

  goToTopic: (topicId) => {
    set((state) => {
      if (state.topicId === null || state.topicId === topicId) {
        return { topicId, open: true };
      }
      return { topicId, open: true, history: [...state.history, state.topicId] };
    });
  },

  browseIndex: () => {
    set((state) => {
      if (state.topicId === null) {
        return state;
      }
      return { topicId: null, history: [...state.history, state.topicId] };
    });
  },

  back: () => {
    set((state) => {
      const previous = state.history[state.history.length - 1];
      if (previous === undefined) {
        return state;
      }
      return { topicId: previous, history: state.history.slice(0, -1) };
    });
  },

  close: () => {
    set({ open: false });
  },

  setQuery: (query) => {
    set({ query });
  },
}));
