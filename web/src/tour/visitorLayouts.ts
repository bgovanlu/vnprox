// T-2802: saved layouts, for a visitor to a public demo.
//
// On a normal instance a layout is app-owned data in the daemon's store,
// written with PUT /layouts/{name} (docs/architecture.md §7's one slot for
// opaque frontend UI state). On a public instance that route is refused at
// the edge like every other write, so dragging a node on the map would
// otherwise throw a 403 at a visitor for doing the most obvious thing there
// is to do with a map.
//
// So in a public demo, and ONLY in a public demo, api/layouts.ts delegates
// here: the same four operations against the edge's per-visitor scratch
// store. That is what makes AC3 ("one visitor's layout changes are invisible
// to another") a property of the product rather than of a test — two
// visitors have two scratch stores, keyed by two opaque cookies, in the
// edge's memory, and neither can address the other's.
//
// EVERY LAYOUT LIVES UNDER ONE SCRATCH KEY, as an object keyed by layout
// name. Not one key per layout: scratch keys are a restricted character set
// and layout names are user-chosen, and any sanitiser mapping the second
// onto the first can collide two differently-named views onto one. One key
// with a JSON object inside has no such failure mode, and keeps the
// per-visitor entry cap meaning what it says.
import { ApiError } from "../api/client";
import type { LayoutListResponse, LayoutResponse, TopologyLayoutPayload } from "../api/types";
import { fetchVisitorState, saveVisitorState } from "./visitorApi";

const VISITOR_LAYOUTS_KEY = "layouts";

/** The blob's type at the storage boundary.
 *
 * Asserted at the fetch, exactly as api/client.ts's `apiFetch<T>` asserts
 * the shape of a JSON body it did not validate either — a layout payload is
 * opaque by contract (docs/api.md's Saved views section: "stored verbatim,
 * opaque JSON"), so there is nothing to narrow it against on either path.
 * Callers that need to know which shape a blob is narrow it themselves with
 * isSavedViewPayload, as they do for the daemon-served list. */
type LayoutStore = Record<string, TopologyLayoutPayload>;

/** This tab's copy of the visitor's layouts, so a save is one round trip
 * rather than a read-modify-write. A public demo visitor has exactly one
 * writer (their own browser), so there is nothing to race with. */
let cached: LayoutStore | null = null;

async function loadAll(): Promise<LayoutStore> {
  cached ??= (await fetchVisitorState<LayoutStore>(VISITOR_LAYOUTS_KEY)) ?? {};
  return cached;
}

/** Test seam: forget this tab's cache. */
export function resetVisitorLayoutCache(): void {
  cached = null;
}

export async function fetchVisitorLayout(name: string): Promise<LayoutResponse> {
  const all = await loadAll();
  const layout = all[name];
  if (layout === undefined) {
    // The same 404 ApiError shape api/layouts.ts documents, so every
    // existing caller's "never saved → use the auto-layout" branch works
    // unchanged.
    throw new ApiError(404, "not_found", "this visitor has no layout saved under that name");
  }
  return { name, layout, updatedAt: 0 };
}

export async function saveVisitorLayout(name: string, layout: TopologyLayoutPayload): Promise<LayoutResponse> {
  const all = await loadAll();
  // Built, saved, and only then adopted as the cache. A save can be refused
  // (413, if this visitor is at their scratch cap), and a cache holding a
  // value the edge does not have would make the next read lie about it.
  const next: LayoutStore = { ...all, [name]: layout };
  await saveVisitorState(VISITOR_LAYOUTS_KEY, next);
  cached = next;
  return { name, layout, updatedAt: 0 };
}

export async function listVisitorLayouts(): Promise<LayoutListResponse> {
  const all = await loadAll();
  return {
    items: Object.entries(all).map(([name, layout]) => ({ name, layout, updatedAt: 0 })),
  };
}

export async function deleteVisitorLayout(name: string): Promise<void> {
  const all = await loadAll();
  if (!(name in all)) return;
  // Rebuilt rather than `delete all[name]`: a dynamic delete on a record
  // keyed by user-chosen names is exactly the pattern eslint's
  // no-dynamic-delete refuses, and the cache is a handful of entries.
  const remaining: LayoutStore = Object.fromEntries(Object.entries(all).filter(([key]) => key !== name));
  await saveVisitorState(VISITOR_LAYOUTS_KEY, remaining);
  cached = remaining;
}
