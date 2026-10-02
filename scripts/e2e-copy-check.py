#!/usr/bin/env python3
"""e2e-copy-check.py -- fail when a diff removes UI copy a Playwright spec
still asserts (#1977).

On the #1970 branch the first `make verify` failed in frontend-e2e on two
specs: one selected the kind option "Automations", the other expected
/only person who sees it/. The diff had changed both strings under ui/src and
updated the vitest tests that asserted them. Nothing before verify reads
ui/e2e, so a literal a diff removes from ui/src surfaced 15+ minutes into
verify and cost a second run.

The check reads the diff against the merge base with main. For every changed
ui/src/**/*.ts(x) file that is not a test, it collects the string literals and
JSX text of the base version and of the working version (the MSW mock data
under ui/src/mocks included: the interactive suite runs against it, so a spec
asserts the names it holds); a text is removed
when the working file holds it fewer times than the base did, so a string
moved within a file or reformatted is not. A text that turns up in another
changed ui/src file (moved between files) is not removed either.

It then reads the string and regex literals of every spec the Playwright
suites run (ui/e2e/interactive and ui/e2e/public-viewer), and fails on each
spec literal that matches a removed text: a regex that finds it, a string
equal to it, or a string of at least MIN_FRAGMENT characters inside it
(Playwright matches a name or text by substring). Each finding names the spec
line and the ui/src file the text left.

A base branch that cannot be resolved fails.
"""

from __future__ import annotations

import os
import re
import subprocess
import sys
from collections import Counter
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
UI_SRC = "ui/src/"
SPEC_DIRS = ("ui/e2e/interactive/", "ui/e2e/public-viewer/")
MIN_FRAGMENT = 8

# A quoted string with no interpolation: '...', "..." or `...` without ${.
STRING = re.compile(r"""'((?:\\.|[^'\\\n])*)'|"((?:\\.|[^"\\\n])*)"|`((?:\\.|[^`\\$\n])*)`""")
# A template literal that interpolates.
TEMPLATE = re.compile(r"`((?:\\.|[^`\\])*\$\{(?:\\.|[^`\\])*)`")
# A comment: a /* */ block, or // at a line start or after whitespace (not the
# // inside a URL string).
COMMENT = re.compile(r"/\*.*?\*/|(?:^|(?<=\s))//[^\n]*", re.DOTALL)
# JSX text between a tag's closing '>' and the next '<' or '{'. The '>' of an
# arrow, a comparison or a generic is not a tag's: it follows '=', '-' or a
# space. Text holding a statement character is code the pattern walked into.
JSX_TEXT = re.compile(r"(?:(?<=[\w\"'}/])|(?<=<))>([^<>{}=;]+)[<{]")
# A regex literal where an expression starts: after ( , = : [ ! & | ? or return.
REGEX = re.compile(r"(?:(?<=[(,=:\[!&|?])|(?<=return))\s*/((?:\\.|\[(?:\\.|[^\]\\])*\]|[^/\\\n\[])+)/([dgimsuy]*)")
LETTER = re.compile(r"[A-Za-z]")
# A regex that asserts copy spells some of it: four letters in a row. /\s+/ or
# /[\d.]+/ in a helper matches any text and asserts none.
WORDY = re.compile(r"(?<!\\)[A-Za-z]{4}")


def git(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["git", *args], cwd=REPO_ROOT, check=check, capture_output=True, text=True)


def merge_base(base_branch: str) -> str:
    for ref in (f"origin/{base_branch}", base_branch):
        if git("rev-parse", "--verify", "--quiet", f"{ref}^{{commit}}", check=False).returncode == 0:
            return git("merge-base", "HEAD", ref).stdout.strip()
    raise SystemExit(
        f"FAIL e2e-copy-check: cannot resolve base branch '{base_branch}' "
        f"(tried origin/{base_branch} and {base_branch}); fetch it or set BASE_BRANCH."
    )


def changed_files(base: str) -> list[str]:
    """Committed, staged, unstaged, untracked and deleted paths."""
    names = set(git("diff", "--name-only", base).stdout.splitlines())
    names.update(git("ls-files", "--others", "--exclude-standard").stdout.splitlines())
    return sorted(n for n in names if n)


def is_ui_source(rel: str) -> bool:
    return (
        rel.startswith(UI_SRC)
        and rel.endswith((".ts", ".tsx"))
        and not re.search(r"\.(test|spec)\.tsx?$", rel)
        and "/test/" not in rel
    )


def base_text(base: str, rel: str) -> str:
    shown = git("show", f"{base}:{rel}", check=False)
    return shown.stdout if shown.returncode == 0 else ""


def work_text(rel: str) -> str:
    path = REPO_ROOT / rel
    return path.read_text(encoding="utf-8") if path.is_file() else ""


def unescape(s: str) -> str:
    return re.sub(r"\\(.)", r"\1", s)


def copy_texts(src: str) -> Counter[str]:
    """The string literals and JSX text in src that could be shown to a person."""
    found: Counter[str] = Counter()
    src = COMMENT.sub("", src)
    for m in STRING.finditer(src):
        text = unescape(next(g for g in m.groups() if g is not None)).strip()
        if len(text) >= 2 and LETTER.search(text):
            found[text] += 1
    for m in JSX_TEXT.finditer(src):
        text = " ".join(m.group(1).split())
        if len(text) >= 2 and LETTER.search(text):
            found[text] += 1
    return found


def ui_corpus(files: list[str]) -> tuple[Counter[str], list[Template]]:
    """Every text and template the working ui/src sources hold."""
    corpus: Counter[str] = Counter()
    patterns: list[Template] = []
    listed = git("ls-files", UI_SRC).stdout.splitlines()
    listed += git("ls-files", "--others", "--exclude-standard", UI_SRC).stdout.splitlines()
    for rel in set(listed) | set(files):
        if is_ui_source(rel):
            src = work_text(rel)
            corpus.update(copy_texts(src))
            patterns.extend(templates(src))
    return corpus, patterns


Template = tuple[re.Pattern[str], int]


def templates(src: str) -> list[Template]:
    """Each interpolated template literal in src as the texts it can render,
    with the length of its fixed part: `Delete ${noun}` renders "Delete script"
    and "Delete library"."""
    out = []
    for m in TEMPLATE.finditer(COMMENT.sub("", src)):
        parts = [unescape(p) for p in re.split(r"\$\{[^}]*\}", m.group(1))]
        if any(LETTER.search(p) for p in parts):
            out.append((re.compile(".+".join(re.escape(p) for p in parts), re.DOTALL), sum(map(len, parts))))
    return out


def rendered(text: str, held: Counter[str], patterns: list[Template]) -> bool:
    """Whether text is held verbatim or rendered by a template whose fixed part
    is at least half of it; `${a} ${b}` renders everything and so nothing."""
    return held[text] > 0 or any(n * 2 >= len(text) and p.fullmatch(text) for p, n in patterns)


def removed_texts(base: str, files: list[str]) -> tuple[dict[str, str], dict[str, str], Counter[str]]:
    """The texts changed ui/src files hold fewer times, each mapped to its file.

    The first map holds a text no ui/src source holds any longer; a spec that
    asserts it cannot pass. The second holds a text one file holds fewer times
    but that is still shown somewhere: the "Automations" filter option of #1970
    went while the section heading of that name stayed, and a spec asserting it
    may be reading either, so it is listed for a look rather than failed.
    """
    gone: dict[str, str] = {}
    left: dict[str, str] = {}
    corpus, corpus_templates = ui_corpus(files)
    for rel in files:
        if not is_ui_source(rel):
            continue
        src = work_text(rel)
        after, after_templates = copy_texts(src), templates(src)
        for text, n in copy_texts(base_text(base, rel)).items():
            if after[text] >= n or (not after[text] and any(k * 2 >= len(text) and p.fullmatch(text) for p, k in after_templates)):
                continue
            (left if rendered(text, corpus, corpus_templates) else gone).setdefault(text, rel)
    return gone, left, corpus


def spec_files() -> list[str]:
    tracked = git("ls-files", *SPEC_DIRS).stdout.splitlines()
    tracked += git("ls-files", "--others", "--exclude-standard", *SPEC_DIRS).stdout.splitlines()
    return sorted({f for f in tracked if f.endswith(".ts") and not Path(f).name.startswith("NOTES_")})


def spec_literals(src: str):
    """Yield (line, kind, literal) for each string and regex literal in a spec."""
    for lineno, line in enumerate(src.splitlines(), 1):
        if line.lstrip().startswith(("//", "*", "import ")):
            continue
        for m in REGEX.finditer(line):
            yield lineno, "regex", (m.group(1), m.group(2))
        for m in STRING.finditer(REGEX.sub("", line)):
            text = unescape(next(g for g in m.groups() if g is not None))
            if len(text) >= 2 and LETTER.search(text):
                yield lineno, "string", text


def matches(kind: str, literal, text: str) -> bool:
    if kind == "regex":
        pattern, flags = literal
        if not WORDY.search(pattern):
            return False
        try:
            rx = re.compile(pattern, re.IGNORECASE if "i" in flags else 0)
        except re.error:
            return False
        return rx.search(text) is not None
    return literal == text or (" " in literal and len(literal) >= MIN_FRAGMENT and literal in text)


def asserts(kind: str, literal, text: str) -> bool:
    """Whether a spec literal reads text as a whole: a string equal to it, or a
    regex that matches all of it. Narrower than matches(), which finds a regex
    anywhere in a sentence, so a short pattern that happens to occur somewhere
    in ui/src does not excuse a spec whose copy was removed."""
    if kind != "regex":
        return literal == text
    pattern, flags = literal
    try:
        rx = re.compile(pattern, re.IGNORECASE if "i" in flags else 0)
    except re.error:
        return False
    return rx.fullmatch(text) is not None


def findings(removed: dict[str, str], shown_now: Counter[str] | None = None) -> tuple[list[str], list[str]]:
    """The spec lines that match a removed text. With shown_now, a spec literal
    that still reads a whole text ui/src shows is returned second, for a look:
    /Open/ matched a removed tooltip that began "Open the print dialog" while a
    button labelled Open was still on the page the spec reads (#1983)."""
    out: list[str] = []
    still: list[str] = []
    for spec in spec_files():
        for lineno, kind, literal in spec_literals(work_text(spec)):
            for text, rel in removed.items():
                if matches(kind, literal, text):
                    shown = f"/{literal[0]}/{literal[1]}" if kind == "regex" else f'"{literal}"'
                    line = f"  {spec}:{lineno}: {shown} matches \"{text}\", which {rel} no longer has"
                    held = shown_now is not None and any(asserts(kind, literal, t) for t in shown_now)
                    (still if held else out).append(line)
                    break
    return out, still


def main() -> int:
    base = merge_base(os.environ.get("BASE_BRANCH", "main"))
    gone, left, corpus = removed_texts(base, changed_files(base))
    if not gone and not left:
        print("e2e-copy-check: no UI copy removed from ui/src against main")
        return 0
    failed, still_matching = findings(gone, corpus)
    moved = findings(left)[0] + still_matching
    if moved:
        print("e2e-copy-check: these specs assert copy that left a changed file but is still shown elsewhere;")
        print("check each still reads the place it means:")
        print("\n".join(moved))
    if not failed:
        print(f"e2e-copy-check: {len(gone)} UI text(s) no longer in ui/src; no e2e spec asserts one")
        return 0
    print("FAIL e2e-copy-check: an e2e spec asserts UI copy this diff removed from ui/src.", file=sys.stderr)
    print("frontend-e2e would fail on each of these; update the spec to the new copy:", file=sys.stderr)
    print("\n".join(failed), file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
