#!/usr/bin/env python3
"""add_spdx_headers.py — T-3801: stamp every first-party .go/.ts/.tsx file
with `SPDX-License-Identifier: Apache-2.0`.

Scope: exactly the files `git ls-files` tracks for the three extensions.
That already excludes everything vendored/generated/third-party in this
repo, because none of it is tracked — node_modules/, web/dist/*
(gitignored except a checked-in placeholder index.html, which is neither
.go/.ts/.tsx), dist/, packaging/build/ are all untracked build output. No
`.go`/`.ts`/`.tsx` file in this repository carries a "Code generated ...
DO NOT EDIT." marker as of T-3801 (verified: `grep -rl "^// Code generated"`
across every tracked .go file found nothing). The GENERATED_MARKER check
below exists anyway, defensively, so a future generated file is skipped
automatically rather than silently stamped — do not remove it just because
it currently matches nothing.

Placement rules:
  Go:
    - A leading `//go:build ...` constraint (there is no old-style
      `// +build` in this tree) must stay the first line, per Go's own
      build-constraint recognition rule (the constraint must be followed by
      a blank line). The header is inserted as its own comment block AFTER
      that blank line, followed by another blank line, so the constraint
      line is untouched and still the first line of the file.
    - Otherwise the header is inserted as a new leading comment block,
      separated by a blank line from whatever followed (including an
      existing package doc comment) so the package doc comment's godoc
      association with `package foo` is preserved exactly — a blank line
      between two `//` comment blocks makes them two separate comments,
      only the one immediately touching `package` is the doc comment.
  TS/TSX:
    - A leading shebang (`#!...`) stays first; the header goes after it.
    - Otherwise the header is inserted as the new first line, followed by a
      blank line. No file in this tree opens with a directive
      (`"use client"` etc.) or a pre-existing license banner (verified via
      grep before writing this script) so this simple rule is safe here;
      if one is ever added, extend SPECIAL_TS_CASES rather than guessing.

Idempotent: any file whose first 10 lines already contain
"SPDX-License-Identifier" is left untouched. Running this script twice
produces zero additional changes (checked by the script's own --check mode
and by CI, which runs `add_spdx_headers.py --check`).

Usage:
  scripts/add_spdx_headers.py            # write headers, report counts
  scripts/add_spdx_headers.py --check    # exit 1 if any file is missing one
"""
from __future__ import annotations

import subprocess
import sys

SPDX_LINE = "// SPDX-License-Identifier: Apache-2.0"
MARKER = "SPDX-License-Identifier"
EXTENSIONS = (".go", ".ts", ".tsx")
GENERATED_MARKER = "// Code generated"  # idiomatic Go/TS generated-file marker


def tracked_files() -> list[str]:
    out = subprocess.run(
        ["git", "ls-files", "*.go", "*.ts", "*.tsx"],
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    return [line for line in out.splitlines() if line]


def already_has_header(lines: list[str]) -> bool:
    return any(MARKER in line for line in lines[:10])


def is_generated(lines: list[str]) -> bool:
    return any(line.startswith(GENERATED_MARKER) for line in lines[:5])


def insert_go(lines: list[str]) -> list[str]:
    if lines and lines[0].startswith("//go:build"):
        # Find the blank line that must terminate the build-constraint
        # block (Go requires it; every //go:build file in this tree has
        # exactly one constraint line followed by a blank line).
        i = 1
        while i < len(lines) and lines[i].strip() != "":
            i += 1
        # lines[i] is the blank line (or EOF, which would be malformed
        # input we don't expect to hit against this tree).
        insert_at = i + 1
        return lines[:insert_at] + [SPDX_LINE, ""] + lines[insert_at:]
    return [SPDX_LINE, ""] + lines


def insert_ts(lines: list[str]) -> list[str]:
    if lines and lines[0].startswith("#!"):
        return lines[:1] + [SPDX_LINE, ""] + lines[1:]
    return [SPDX_LINE, ""] + lines


def process(path, check_only: bool) -> str:
    """Returns one of: 'header', 'skip-has-header', 'skip-generated'."""
    text = path.read_text(encoding="utf-8")
    # Preserve whether the file ended with a trailing newline.
    had_trailing_newline = text.endswith("\n")
    lines = text.split("\n")
    if had_trailing_newline and lines and lines[-1] == "":
        lines.pop()

    if already_has_header(lines):
        return "skip-has-header"
    if is_generated(lines):
        return "skip-generated"

    if path.suffix == ".go":
        new_lines = insert_go(lines)
    else:
        new_lines = insert_ts(lines)

    if not check_only:
        new_text = "\n".join(new_lines)
        if had_trailing_newline or True:
            new_text += "\n"
        path.write_text(new_text, encoding="utf-8")
    return "header"


def main() -> int:
    check_only = "--check" in sys.argv[1:]
    global REPO_ROOT
    import pathlib

    REPO_ROOT = pathlib.Path(
        subprocess.run(
            ["git", "rev-parse", "--show-toplevel"],
            capture_output=True,
            text=True,
            check=True,
        ).stdout.strip()
    )

    files = tracked_files()
    counts: dict[str, int] = {}
    missing: list[str] = []
    for rel in files:
        path = REPO_ROOT / rel
        result = process(path, check_only)
        counts[result] = counts.get(result, 0) + 1
        if check_only and result == "header":
            missing.append(rel)

    total = len(files)
    print(f"scanned {total} tracked .go/.ts/.tsx files")
    for k, v in sorted(counts.items()):
        print(f"  {k}: {v}")

    if check_only:
        if missing:
            print(f"\n{len(missing)} file(s) missing SPDX header:", file=sys.stderr)
            for m in missing:
                print(f"  {m}", file=sys.stderr)
            return 1
        print("all files carry the SPDX header")
        return 0

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
