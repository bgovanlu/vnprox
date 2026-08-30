// SPDX-License-Identifier: Apache-2.0

import { Fragment } from "react";
import { tokenizeInline } from "./inline";

export interface HelpTextProps {
  children: string;
}

/** Renders help prose's two inline markers as real elements. Deliberately
 * builds React nodes rather than an HTML string — no help content ever
 * reaches dangerouslySetInnerHTML, so a future content contribution can't
 * become an injection vector. */
export function HelpText({ children }: HelpTextProps) {
  return (
    <>
      {tokenizeInline(children).map((token, i) => {
        // Tokens have no identity of their own and the list is derived
        // fresh from an immutable string on every render, so the index is
        // a stable key here.
        const key = `${String(i)}:${token.kind}`;
        if (token.kind === "bold") {
          return (
            <strong key={key} className="font-semibold text-fg">
              {token.text}
            </strong>
          );
        }
        if (token.kind === "code") {
          return (
            <code
              key={key}
              className="rounded bg-slate-100 px-1 py-0.5 font-mono text-[0.85em] text-slate-800 dark:bg-slate-800 dark:text-slate-200"
            >
              {token.text}
            </code>
          );
        }
        return <Fragment key={key}>{token.text}</Fragment>;
      })}
    </>
  );
}
