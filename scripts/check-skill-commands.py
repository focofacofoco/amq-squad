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

# A command reference must START its line. Occurrences mid-sentence are prose:
# "This project uses amq-squad for agent team coordination" is English, and the
# fences in these skills legitimately contain file templates as well as shell.
COMMAND_LINE = re.compile(r"^\s*(?:\$\s+|&&\s+|\|\s+)?amq-squad\s+(?P<rest>[^\n]*)", re.M)

FENCE = re.compile(r"```.*?```", re.S)
# Inline spans WRAP ACROSS LINES in these files (cli/SKILL.md names
# `amq-squad evidence run TASK --profile ...` broken over two lines), so the span
# body must tolerate newlines. A newline-hostile pattern misses it SILENTLY, which
# is the dangerous kind of miss.
INLINE = re.compile(r"`([^`]+)`", re.S)

VERB = re.compile(r"^[a-z][a-z0-9-]*$")
FLAG = re.compile(r"^--[a-z][a-z0-9-]*$")

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
    blobs = [m.group(0) for m in FENCE.finditer(text)]
    blobs.extend(m.group(1) for m in INLINE.finditer(FENCE.sub("", text)))
    return blobs


def extract(paths: list[Path]) -> dict[tuple[str, str | None], set[str]]:
    """Map (verb, subcommand-or-None) -> set of flags named with it, per skills."""
    found: dict[tuple[str, str | None], set[str]] = {}
    for path in paths:
        for blob in code_blobs(path.read_text()):
            for match in COMMAND_LINE.finditer(blob):
                tokens = match.group("rest").split()
                if not tokens:
                    continue
                # Skip only a leading FLAG: `amq-squad --version` is legitimate and
                # names no verb. Anything ELSE in the verb position is a CLAIMED
                # command and must be checked, even when it does not look like a
                # valid verb.
                #
                # An earlier version skipped any token failing the lowercase VERB
                # pattern. That meant renaming a command to `evidenceX` made the
                # reference VANISH rather than fail: the extracted count silently
                # dropped from 11 to 10 and the gate reported success. A drift gate
                # that discards references it cannot parse is worse than no gate,
                # because the discard is indistinguishable from agreement.
                if tokens[0].startswith("-"):
                    continue
                verb = tokens[0]
                sub = tokens[1] if len(tokens) > 1 and VERB.match(tokens[1]) else None
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
    verified_real_verb = False
    for (verb, sub), flags in sorted(checked.items()):
        if verb not in surface:
            failures.append(f"verb 'amq-squad {verb}' is named in the skills but is not a command")
            continue
        verified_real_verb = True
        known, observable = flag_surface(binary, verb, sub)
        target = f"{verb} {sub}" if sub else verb
        if not observable:
            if flags:
                unverifiable.append(f"amq-squad {target} ({len(flags)} flag(s): {', '.join(sorted(flags))})")
            continue
        flags_checked += len(flags)
        for flag in sorted(flags):
            if flag not in known:
                failures.append(f"flag '{flag}' is named for 'amq-squad {target}' but that command does not accept it")

    if not verified_real_verb:
        print("error: no real verb was verified; the gate checked nothing", file=sys.stderr)
        return 2

    if failures:
        print(f"skill/command drift ({len(failures)}):", file=sys.stderr)
        for f in failures:
            print(f"  - {f}", file=sys.stderr)
        print("\nFix the skill text, or the command, so they agree.", file=sys.stderr)
        return 1

    # Report coverage honestly: a gate that quietly verifies less than it claims is
    # the failure mode this whole file exists to prevent.
    print(f"skills name {len(checked)} command(s); all {len(checked)} verb(s) resolve, {flags_checked} flag(s) verified")
    if unverifiable:
        print(f"flags NOT verifiable from --help for {len(unverifiable)} command(s) "
              "(help errors without required positionals, or delegates its flag set):")
        for u in unverifiable:
            print(f"  - {u}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
