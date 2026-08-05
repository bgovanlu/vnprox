// The help registry: merges the content modules into one lookup, and is the
// single place anything in the app resolves a topic id from.
import type { HelpTopic } from "./types";
import { plainText } from "./inline";
import { CONCEPT_TOPICS } from "./content/concepts";
import { PAGE_TOPICS } from "./content/pages";
import { PANEL_TOPICS } from "./content/panels";
import { PLATFORM_TOPICS } from "./content/platform";

const ALL: readonly HelpTopic[] = [
  ...PAGE_TOPICS,
  ...CONCEPT_TOPICS,
  ...PANEL_TOPICS,
  ...PLATFORM_TOPICS,
];

// Built eagerly at module load so a duplicate id is a hard, immediate
// failure naming the offender — not a topic that silently shadows another
// one at whichever call site happens to look it up first.
const BY_ID: ReadonlyMap<string, HelpTopic> = (() => {
  const map = new Map<string, HelpTopic>();
  for (const topic of ALL) {
    if (map.has(topic.id)) {
      throw new Error(`duplicate help topic id: ${topic.id}`);
    }
    map.set(topic.id, topic);
  }
  return map;
})();

/** Resolves a topic id, or undefined. Never throws — a stale id in a
 * bookmarked `?help=` URL should render "no such topic", not blank the
 * page. */
export function getHelpTopic(id: string): HelpTopic | undefined {
  return BY_ID.get(id);
}

/** Every topic, in registration order (pages, then concepts, then panels,
 * then platform). */
export function allHelpTopics(): readonly HelpTopic[] {
  return ALL;
}

export interface HelpSearchHit {
  readonly topic: HelpTopic;
  /** Higher is better. Exposed so the panel can show a relevance order
   * without re-deriving one. */
  readonly score: number;
  /** The section heading the match came from, when it wasn't the title or
   * summary — shown under the hit so a body match doesn't look arbitrary. */
  readonly matchedIn?: string;
}

/** Case-insensitive search over title, summary, section headings and
 * bodies, and keywords. Every term must appear somewhere in the topic
 * (AND, not OR) — a two-word query that matches nothing precise is more
 * useful returning nothing than returning half the manual. */
export function searchHelp(query: string): readonly HelpSearchHit[] {
  const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (terms.length === 0) {
    return [];
  }

  const hits: HelpSearchHit[] = [];
  for (const topic of ALL) {
    const title = topic.title.toLowerCase();
    const summary = plainText(topic.summary).toLowerCase();
    const keywords = (topic.keywords ?? []).join(" ").toLowerCase();
    const sections = topic.sections.map((s) => ({
      heading: s.heading,
      text: `${s.heading} ${plainText(s.body)}`.toLowerCase(),
    }));
    const steps = (topic.steps ?? []).map((s) => plainText(s).toLowerCase()).join(" ");

    let score = 0;
    let matchedIn: string | undefined;
    let matchedEveryTerm = true;

    for (const term of terms) {
      if (title.includes(term)) {
        score += title === term ? 100 : 40;
      } else if (topic.id.includes(term)) {
        score += 30;
      } else if (keywords.includes(term)) {
        score += 25;
      } else if (summary.includes(term)) {
        score += 15;
      } else {
        const section = sections.find((s) => s.text.includes(term));
        if (section) {
          score += 5;
          matchedIn ??= section.heading;
        } else if (steps.includes(term)) {
          score += 5;
        } else {
          matchedEveryTerm = false;
          break;
        }
      }
    }

    if (matchedEveryTerm) {
      hits.push({ topic, score, matchedIn });
    }
  }

  // Ties broken by title so results are stable between renders rather than
  // depending on registration order.
  return hits.sort((a, b) => b.score - a.score || a.topic.title.localeCompare(b.topic.title));
}
