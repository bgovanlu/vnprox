import { useMemo } from "react";
import { Drawer, DrawerContent, DrawerTitle, DrawerDescription } from "../components/Drawer";
import { Button } from "../components/Button";
import { HelpText } from "./HelpText";
import { useHelpStore, helpOpenerElement } from "./store";
import { getHelpTopic, searchHelp, allHelpTopics } from "./registry";
import type { HelpSurface, HelpTopic } from "./types";

const SURFACE_LABEL: Record<HelpSurface, string> = {
  page: "Screens",
  panel: "Panels & tools",
  dialog: "Dialogs & wizards",
  concept: "How vnprox works",
  reference: "Reference",
};

// Browse order: what a lost user wants first. Concepts lead, because "why
// won't this apply" is answered by the safety model far more often than by
// any one screen's page.
const SURFACE_ORDER: readonly HelpSurface[] = ["concept", "page", "panel", "dialog", "reference"];

function TopicBody({ topic }: { topic: HelpTopic }) {
  const goToTopic = useHelpStore((s) => s.goToTopic);
  return (
    <div className="mt-4 space-y-5">
      {topic.sections.map((section) => (
        <section key={section.heading}>
          <h3 className="text-sm font-semibold text-slate-900 dark:text-slate-100">{section.heading}</h3>
          <p className="mt-1 text-sm leading-relaxed text-slate-600 dark:text-slate-300">
            <HelpText>{section.body}</HelpText>
          </p>
        </section>
      ))}

      {topic.steps && topic.steps.length > 0 && (
        <section>
          <h3 className="text-sm font-semibold text-slate-900 dark:text-slate-100">Step by step</h3>
          <ol className="mt-1 list-decimal space-y-1 pl-5 text-sm leading-relaxed text-slate-600 dark:text-slate-300">
            {topic.steps.map((step) => (
              <li key={step}>
                <HelpText>{step}</HelpText>
              </li>
            ))}
          </ol>
        </section>
      )}

      {topic.seeAlso && topic.seeAlso.length > 0 && (
        <section>
          <h3 className="text-xs font-semibold uppercase tracking-wide text-slate-400 dark:text-slate-500">
            See also
          </h3>
          <ul className="mt-2 flex flex-wrap gap-2">
            {topic.seeAlso.map((id) => {
              const related = getHelpTopic(id);
              if (!related) {
                return null;
              }
              return (
                <li key={id}>
                  <button
                    type="button"
                    onClick={() => {
                      goToTopic(id);
                    }}
                    className="rounded-full border border-slate-300 px-2.5 py-1 text-xs text-slate-600 hover:border-slate-400 hover:text-slate-900 dark:border-slate-700 dark:text-slate-300 dark:hover:border-slate-500 dark:hover:text-slate-100"
                  >
                    {related.title}
                  </button>
                </li>
              );
            })}
          </ul>
        </section>
      )}

      <p className="border-t border-slate-200 pt-3 text-xs text-slate-400 dark:border-slate-800 dark:text-slate-500">
        Written from <code className="font-mono">{topic.docRef}</code> in the vnprox repository.
      </p>
    </div>
  );
}

function SearchResults({ query }: { query: string }) {
  const goToTopic = useHelpStore((s) => s.goToTopic);
  const hits = useMemo(() => searchHelp(query), [query]);

  if (hits.length === 0) {
    return (
      <p className="mt-6 text-sm text-slate-500 dark:text-slate-400">
        Nothing matches “{query}”. Try a single word — an interface name, a screen, or what you're trying to
        do.
      </p>
    );
  }

  return (
    <ul className="mt-4 space-y-1">
      {hits.map((hit) => (
        <li key={hit.topic.id}>
          <button
            type="button"
            onClick={() => {
              goToTopic(hit.topic.id);
            }}
            className="w-full rounded-md px-2 py-2 text-left hover:bg-slate-100 dark:hover:bg-slate-800"
          >
            <span className="block text-sm font-medium text-slate-900 dark:text-slate-100">
              {hit.topic.title}
            </span>
            <span className="mt-0.5 block text-xs leading-relaxed text-slate-500 dark:text-slate-400">
              {hit.topic.summary}
            </span>
            {hit.matchedIn !== undefined && (
              <span className="mt-1 block text-xs text-slate-400 dark:text-slate-500">
                matched in “{hit.matchedIn}”
              </span>
            )}
          </button>
        </li>
      ))}
    </ul>
  );
}

function BrowseIndex() {
  const goToTopic = useHelpStore((s) => s.goToTopic);
  const grouped = useMemo(() => {
    const bySurface = new Map<HelpSurface, HelpTopic[]>();
    for (const topic of allHelpTopics()) {
      const list = bySurface.get(topic.surface) ?? [];
      list.push(topic);
      bySurface.set(topic.surface, list);
    }
    return bySurface;
  }, []);

  return (
    <div className="mt-6 space-y-5">
      {SURFACE_ORDER.map((surface) => {
        const topics = grouped.get(surface);
        if (!topics || topics.length === 0) {
          return null;
        }
        return (
          <section key={surface}>
            <h3 className="text-xs font-semibold uppercase tracking-wide text-slate-400 dark:text-slate-500">
              {SURFACE_LABEL[surface]}
            </h3>
            <ul className="mt-2 space-y-0.5">
              {topics.map((topic) => (
                <li key={topic.id}>
                  <button
                    type="button"
                    onClick={() => {
                      goToTopic(topic.id);
                    }}
                    className="w-full rounded px-2 py-1 text-left text-sm text-slate-600 hover:bg-slate-100 hover:text-slate-900 dark:text-slate-300 dark:hover:bg-slate-800 dark:hover:text-slate-100"
                  >
                    {topic.title}
                  </button>
                </li>
              ))}
            </ul>
          </section>
        );
      })}
    </div>
  );
}

/** The help surface: a right-hand drawer showing the current screen's
 * topic, with search across every topic and a browse index. Mounted once
 * in AppShell; opened from the top bar, from `F1`, or from any
 * <HelpAnchor> a feature module places next to a panel heading. */
export function HelpPanel() {
  const open = useHelpStore((s) => s.open);
  const topicId = useHelpStore((s) => s.topicId);
  const history = useHelpStore((s) => s.history);
  const query = useHelpStore((s) => s.query);
  const setQuery = useHelpStore((s) => s.setQuery);
  const back = useHelpStore((s) => s.back);
  const browseIndex = useHelpStore((s) => s.browseIndex);
  const close = useHelpStore((s) => s.close);

  const topic = topicId === null ? undefined : getHelpTopic(topicId);
  const searching = query.trim().length > 0;

  return (
    <Drawer
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          close();
        }
      }}
    >
      <DrawerContent
        side="right"
        aria-describedby="help-panel-summary"
        className="max-w-lg"
        // Radix's own restore targets a <DialogTrigger>, which this panel
        // deliberately doesn't have (see store.ts) — so take over and put
        // focus back on whatever actually opened it.
        onCloseAutoFocus={(event) => {
          const target = helpOpenerElement();
          if (target) {
            event.preventDefault();
            target.focus();
          }
        }}
      >
        <div className="flex items-start gap-2">
          {history.length > 0 && !searching && (
            <Button variant="ghost" size="sm" onClick={back} aria-label="Back">
              ←
            </Button>
          )}
          <div className="min-w-0 flex-1">
            <DrawerTitle className="text-base font-semibold text-slate-900 dark:text-slate-100">
              {searching ? "Search help" : (topic?.title ?? "Help")}
            </DrawerTitle>
            <DrawerDescription
              id="help-panel-summary"
              className="mt-1 text-sm leading-relaxed text-slate-500 dark:text-slate-400"
            >
              {searching
                ? `Results for “${query}”.`
                : (topic?.summary ??
                  "Pick a topic below, or search. Press Escape to close and F1 to reopen on any screen.")}
            </DrawerDescription>
          </div>
        </div>

        <label className="mt-4 block">
          <span className="sr-only">Search help</span>
          <input
            type="search"
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
            }}
            placeholder="Search help…"
            className="h-9 w-full rounded-md border border-slate-300 bg-white px-3 text-sm text-slate-900 placeholder:text-slate-400 focus:border-slate-400 focus:outline-none dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100 dark:placeholder:text-slate-500"
          />
        </label>

        {searching ? (
          <SearchResults query={query.trim()} />
        ) : topic ? (
          <TopicBody topic={topic} />
        ) : (
          <BrowseIndex />
        )}

        {/* A single navigation affordance rather than an inline copy of the
         * index: rendering every topic title twice in one panel makes the
         * whole list ambiguous to anything querying by name — a screen
         * reader as much as a test. */}
        {!searching && topic && (
          <div className="mt-6 border-t border-slate-200 pt-3 dark:border-slate-800">
            <button
              type="button"
              onClick={browseIndex}
              className="text-xs font-semibold uppercase tracking-wide text-slate-400 hover:text-slate-600 dark:text-slate-500 dark:hover:text-slate-300"
            >
              Browse all help topics →
            </button>
          </div>
        )}
      </DrawerContent>
    </Drawer>
  );
}
