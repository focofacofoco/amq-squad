#!/usr/bin/env python3
"""Fail the build when a skill names a CLI command or flag the binary does not have.

#534: the skills drift from the binary between releases, and the drift is only
found when an operator follows an instruction that no longer works. This converts
that from a recurring release-note problem into a build failure, and it is the
prerequisite for #522's verb-grammar rewrite: with this gate in place that rewrite
becomes a mechanical regeneration instead of a manual audit.

The command surface is read from the binary's own --help output, so there is no
second list of verbs to keep in sync. That is deliberate: a hand-maintained mirror
of the command surface would be the very defect this gate exists to catch.
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
SKILLS = REPO / "plugins" / "skills-src"

# A command reference must START its line, or appear inside a Bash(...) permission
# string. Occurrences mid-sentence are prose: "This project uses amq-squad for
# agent team coordination" is English, and the fences in these skills legitimately
# contain file templates as well as shell.
#
# MEDIUM 4: Bash(amq-squad review-worktree remove:*) is a REAL command reference --
# it is what an operator puts in an allowlist -- and the line-start rule alone
# ignored it, so renames could break permission strings silently.
COMMAND_LINE = re.compile(
    r"(?:^\s*(?:\$\s+|&&\s+|\|\s+)?|\bBash\(\s*)amq-squad\s+(?P<rest>[^\n]*)", re.M
)

# A trailing permission-string suffix: Bash(amq-squad x remove:*) -> drop ":*)".
PERMISSION_SUFFIX = re.compile(r"[:)].*$")

# Continuation joining (HIGH 2). A reference wraps in these files:
#   `amq-squad evidence run TASK --profile PROFILE --session SESSION --me ACTOR
#   --subject TEXT --attempt-id ID -- COMMAND [ARG...]`
# Capturing only to the first newline SILENTLY dropped every flag after the wrap
# (--subject, --attempt-id, and in other references --lead, --launch-shape, --go).
# Silently dropping flags is the same class as dropping whole references: the gate
# reports agreement about text it never read.
# In a FENCE, only a genuine continuation is joined: a trailing backslash, or a
# following line indented under the command. Joining unconditionally would merge
# distinct commands that simply sit on consecutive lines.
FENCE_CONTINUATION = re.compile(r"\\\n\s*|\n[ \t]+(?=--)")

FENCE = re.compile(r"```.*?```", re.S)
# Inline spans WRAP ACROSS LINES in these files (cli/SKILL.md names
# `amq-squad evidence run TASK --profile ...` broken over two lines), so the span
# body must tolerate newlines. A newline-hostile pattern misses it SILENTLY, which
# is the dangerous kind of miss.
INLINE = re.compile(r"`([^`]+)`", re.S)

VERB = re.compile(r"^[a-z][a-z0-9-]*$")
# A subcommand token may be malformed; we still check it rather than drop it.
SUBCOMMAND_TOKEN = re.compile(r"^[A-Za-z][A-Za-z0-9-]*$")
# Sentinel key for a flag-only reference such as `amq-squad --version`.
GLOBAL_FLAG_KEY = "<global-flags>"
# A flag token may be MALFORMED and must still be extracted so it can be reported.
# A lowercase-only pattern silently dropped `--launch-shapeX`, which is the
# vanishing-reference class a third time: fixed in the verb position, found by
# review in the subcommand position, and still live in the flag position until a
# test caught it. Extract anything flag-shaped; let verification judge it.
FLAG = re.compile(r"^--[A-Za-z][A-Za-z0-9-]*$")

# Strings that are formatted as commands but are not commands.
#
# This list must stay EMPTY-able: every entry is a concession, and
# test_check_skill_commands.py asserts each one still actually appears in the
# skills, so a concession cannot outlive its cause silently.
NOT_A_COMMAND = {
    # The version-announcement preamble renders an identity string in backticks:
    # "stating the loaded identity as `amq-squad skill v2.24.0`". #534's skill
    # rewrite removes that preamble entirely, at which point this entry must go.
    "skill": "version-announcement identity string, not a command; removed by #534's preamble deletion",
}

# Anti-vacuity floors. A gate that silently finds nothing is worse than no gate:
# it reports success while checking zero things, which is exactly the state the
# skills are in before they name commands.
MIN_COMMANDS = 8
MIN_VERBS_IN_SURFACE = 20
# Below this many claimed flags the zero-verified floor would be noise; above it,
# verifying none of them means flag parsing has broken.
MIN_FLAGS_CLAIMED_FOR_FLOOR = 4


def run_help(binary: str, *args: str) -> str:
    proc = subprocess.run([binary, *args, "--help"], capture_output=True, text=True)
    return proc.stdout + proc.stderr


def verb_surface(binary: str) -> set[str]:
    """Top-level verbs, parsed from the binary's own two-column help."""
    text = run_help(binary)
    return {m for m in re.findall(r"^\s{2,}([a-z][a-z0-9-]{1,30})\s{2,}\S", text, re.M)}


# Help text that DELEGATES its flags rather than listing them, e.g.
#   amq-squad wizard [run start prefill flags] [--scope project|global]
#   amq-squad new profile NAME ... [team init options]
# A command like that accepts flags its own help never enumerates, so its flag set
# is not observable from --help and must not be treated as exhaustive.
DELEGATES_FLAGS = re.compile(r"\[[^\]]*\b(?:flags|options)\]")


# The binary names its own valid subcommands when given a bad one:
#   error: unknown 'team' subcommand: "synchronise". Try 'init', 'resume', ...
# That is a DEFINITIVE negative, so it is the one subcommand error we interpret --
# the same discipline as t2's single interpretable git error. Everything else stays
# uninterpretable rather than being optimistically treated as fine.
UNKNOWN_SUBCOMMAND = re.compile(r"unknown '([^']+)' subcommand", re.I)


def subcommand_exists(binary: str, verb: str, sub: str) -> tuple[bool, bool]:
    """Return (exists, definitive).

    HIGH 1: the gate previously verified only the TOP-LEVEL verb, so
    `amq-squad team syncX` passed because `team` resolves and the resulting help
    error was filed as merely unverifiable. Subcommands are where most renames
    happen, so that hole voided the gate's core promise.
    """
    text = run_help(binary, verb, sub)
    if UNKNOWN_SUBCOMMAND.search(text):
        return False, True
    return True, True


def flag_surface(binary: str, verb: str, sub: str | None) -> tuple[set[str], bool]:
    """Return (flags, observable).

    observable is False when the exact command's help cannot be trusted as an
    exhaustive flag list: either it errored (some subcommands refuse --help without
    required positionals, e.g. `evidence run`) or it delegates its flags to another
    command's set. In those cases flags are UNVERIFIABLE, not absent.

    Falling back to the PARENT's help would be worse than not checking: `evidence`
    and `evidence run` have different flag sets, so the parent's list produced
    confident false positives for flags that do work.
    """
    args = [verb] if sub is None else [verb, sub]
    text = run_help(binary, *args)
    if text.lstrip().startswith("error:"):
        return set(), False
    if DELEGATES_FLAGS.search(text):
        return set(), False
    flags = set(re.findall(r"(--[a-z][a-z0-9-]*)", text))
    return flags, bool(flags)


def code_blobs(text: str) -> list[str]:
    """Return code blobs already normalized for whole-reference extraction.

    HIGH 2: an inline span is ONE logical command even when the markdown wraps it
    mid-flag-list, and the wrap in cli/SKILL.md has NO leading whitespace on the
    continuation line:

        `amq-squad evidence run TASK --profile P --session S --me ACTOR
        --subject TEXT --attempt-id ID -- COMMAND [ARG...]`

    so a continuation rule requiring indentation missed it and every flag after the
    wrap was silently dropped. Inline spans therefore collapse ALL newlines; fences
    join only genuine continuations, because consecutive lines in a fence are
    usually separate commands.
    """
    blobs = [FENCE_CONTINUATION.sub(" ", m.group(0)) for m in FENCE.finditer(text)]
    blobs.extend(
        re.sub(r"\s*\n\s*", " ", m.group(1)) for m in INLINE.finditer(FENCE.sub("", text))
    )
    return blobs


def extract(paths: list[Path]) -> dict[tuple[str, str | None], set[str]]:
    """Map (verb, subcommand-or-None) -> set of flags named with it, per skills."""
    found: dict[tuple[str, str | None], set[str]] = {}
    for path in paths:
        for blob in code_blobs(path.read_text()):
            # Join wrapped continuation lines so a reference is read whole.
            for match in COMMAND_LINE.finditer(blob):
                rest = PERMISSION_SUFFIX.sub("", match.group("rest"))
                tokens = rest.split()
                if not tokens:
                    continue
                verb = tokens[0]
                # MEDIUM 3: a leading-dash token must not be discarded wholesale.
                # `amq-squad --version` is a legitimate flag-only reference; a
                # malformed verb like `-doctor`, or a global flag that does not
                # exist, is a CLAIM that must be checked. Blanket-skipping anything
                # starting with "-" was the vanishing-reference class surviving in
                # the flag branch after being fixed in the verb branch.
                if verb.startswith("-"):
                    if len(tokens) == 1 and verb.startswith("--"):
                        found.setdefault((GLOBAL_FLAG_KEY, None), set()).add(verb)
                    else:
                        found.setdefault((verb, None), set())
                    continue
                sub = tokens[1] if len(tokens) > 1 and not tokens[1].startswith("-") else None
                if sub is not None and not SUBCOMMAND_TOKEN.match(sub):
                    # A malformed subcommand (e.g. `syncX`) must be REPORTED, not
                    # silently folded into the bare verb: folding both loses the
                    # rename and, when the same verb also appears with a real
                    # subcommand, produced a None/str sort crash.
                    sub = sub.lower() if sub.isalpha() else sub
                flags = {t for t in tokens if FLAG.match(t)}
                found.setdefault((verb, sub), set()).update(flags)
    return found


def main() -> int:
    binary = sys.argv[1] if len(sys.argv) > 1 else str(REPO / "amq-squad")
    # Optional second argument: the skills root. Defaults to this repo's sources.
    # It exists so the no-skills-found branch is testable without faking the repo
    # layout, and so the gate can be pointed at another checkout.
    skills = Path(sys.argv[2]) if len(sys.argv) > 2 else SKILLS
    if not Path(binary).exists():
        print(f"error: binary not found at {binary}; run 'make build' first", file=sys.stderr)
        return 2

    surface = verb_surface(binary)
    if len(surface) < MIN_VERBS_IN_SURFACE:
        print(
            f"error: only {len(surface)} verbs parsed from '{binary} --help' "
            f"(expected >= {MIN_VERBS_IN_SURFACE}). The help format probably changed; "
            "this gate would otherwise pass by checking against an empty surface.",
            file=sys.stderr,
        )
        return 2

    paths = sorted(skills.rglob("*.md"))
    if not paths:
        print(f"error: no skill sources found under {skills}", file=sys.stderr)
        return 2

    found = extract(paths)
    checked = {k: v for k, v in found.items() if k[0] not in NOT_A_COMMAND}
    if len(checked) < MIN_COMMANDS:
        print(
            f"error: extracted only {len(checked)} command references from the skills "
            f"(expected >= {MIN_COMMANDS}). Either the skills stopped naming commands or the "
            "extractor broke; both must fail rather than silently check nothing.",
            file=sys.stderr,
        )
        return 2

    failures: list[str] = []
    unverifiable: list[str] = []
    flags_checked = 0
    flags_claimed = 0
    verified_real_verb = False

    global_flags, _ = flag_surface(binary, "", None)

    # Sort with a None-safe key. Sorting raw (verb, sub) tuples crashed with
    # TypeError whenever one verb appeared both bare and with a subcommand, because
    # None is not comparable to str. That crash surfaced while probing HIGH 1 and is
    # its own defect: a gate that dies on a legitimate corpus shape is unusable.
    for (verb, sub), flags in sorted(checked.items(), key=lambda kv: (kv[0][0], kv[0][1] or "")):
        flags_claimed += len(flags)

        # A flag-only reference (`amq-squad --version`): verify the flags exist.
        if verb == GLOBAL_FLAG_KEY:
            for flag in sorted(flags):
                if global_flags and flag not in global_flags:
                    failures.append(f"global flag '{flag}' is named in the skills but 'amq-squad --help' does not list it")
                else:
                    flags_checked += 1
            continue

        if verb.startswith("-"):
            failures.append(f"'amq-squad {verb}' is named in the skills but is not a command or a global flag")
            continue

        if verb not in surface:
            failures.append(f"verb 'amq-squad {verb}' is named in the skills but is not a command")
            continue
        verified_real_verb = True

        # HIGH 1: verify the FULL command path, not just the first token.
        if sub is not None:
            exists, definitive = subcommand_exists(binary, verb, sub)
            if definitive and not exists:
                failures.append(f"subcommand 'amq-squad {verb} {sub}' is named in the skills but {verb} has no such subcommand")
                continue

        target = f"{verb} {sub}" if sub else verb
        known, observable = flag_surface(binary, verb, sub)
        if not observable:
            if flags:
                unverifiable.append(f"amq-squad {target} ({len(flags)} flag(s): {', '.join(sorted(flags))})")
            continue
        for flag in sorted(flags):
            if flag not in known:
                failures.append(f"flag '{flag}' is named for 'amq-squad {target}' but that command does not accept it")
            else:
                flags_checked += 1

    if not verified_real_verb:
        print("error: no real verb was verified; the gate checked nothing", file=sys.stderr)
        return 2

    # MEDIUM 5: floor the FLAG coverage too. Verbs were floored; flags were not, so
    # a help-format change that stopped yielding parsed flags would make everything
    # "unverifiable" and keep the build green -- the same silent shrink, one level
    # down.
    if flags_claimed >= MIN_FLAGS_CLAIMED_FOR_FLOOR and flags_checked == 0:
        print(
            f"error: {flags_claimed} flag(s) are named in the skills but ZERO were verified. "
            "Flag help is probably no longer parseable; this gate would otherwise pass by "
            "verifying no flags at all.",
            file=sys.stderr,
        )
        return 2

    if failures:
        print(f"skill/command drift ({len(failures)}):", file=sys.stderr)
        for f in failures:
            print(f"  - {f}", file=sys.stderr)
        print("\nFix the skill text, or the command, so they agree.", file=sys.stderr)
        return 1

    print(f"skills name {len(checked)} command(s); all verb/subcommand paths resolve, {flags_checked} of {flags_claimed} flag(s) verified")
    if unverifiable:
        print(f"flags NOT verifiable from --help for {len(unverifiable)} command(s) "
              "(help errors without required positionals, or delegates its flag set):")
        for u in unverifiable:
            print(f"  - {u}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
