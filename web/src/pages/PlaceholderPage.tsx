// SPDX-License-Identifier: Apache-2.0

import { EmptyState } from "../components/EmptyState";

export interface PlaceholderPageProps {
  title: string;
  description: string;
}

/** Shared render for the eight top-level routes T-005 scaffolds but
 * doesn't implement — each later feature task replaces its own page's
 * body with the real thing. */
export function PlaceholderPage({ title, description }: PlaceholderPageProps) {
  return (
    <div className="flex h-full flex-col gap-4">
      <h1 className="text-xl font-semibold">{title}</h1>
      <EmptyState title={`${title} — coming soon`} description={description} />
    </div>
  );
}
