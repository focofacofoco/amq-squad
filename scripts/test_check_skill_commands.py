#!/usr/bin/env python3
"""Tests for the #534 skill/command drift gate.

The gate's whole value is that it FAILS on drift, so these tests mutate a skill
and assert the exit code changes. A drift gate is worthless if never proven to
detect drift, and one specific bug found while writing these tests proves the
point: a rename containing an uppercase letter used to make the reference VANISH
from extraction (count silently dropped, exit 0) rather than fail.
"""

from __future__ import annotations

import importlib.util
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
GATE = REPO / "scripts" / "check-skill-commands.py"
BINARY = REPO / "amq-squad"
SKILLS = REPO / "plugins" / "skills-src"


def load_gate():
    spec = importlib.util.spec_from_file_location("gate", GATE)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def run_gate() -> subprocess.CompletedProcess:
    return subprocess.run(
        [sys.executable, str(GATE), str(BINARY)], capture_output=True, text=True
    )


class MutatedSkill:
    """Rewrite one skill file for the duration of a test, then restore it byte-for-byte."""

    def __init__(self, path: Path, old: str, new: str):
        self.path, self.old, self.new = path, old, new

    def __enter__(self):
        self.original = self.path.read_bytes()
        text = self.original.decode()
        if self.old not in text:
            raise AssertionError(
                f"fixture is wrong: {self.path.name} does not contain {self.old!r}. "
                "A mutation that misses its target proves nothing."
            )
        self.path.write_text(text.replace(self.old, self.new))
        return self

    def __exit__(self, *exc):
        self.path.write_bytes(self.original)
        return False


class GatePassesOnCleanTree(unittest.TestCase):
    def test_clean_tree_passes(self):
        if not BINARY.exists():
            self.skipTest("binary not built; run make build")
        result = run_gate()
        self.assertEqual(result.returncode, 0, f"gate failed on a clean tree:\n{result.stderr}")

    def test_reports_what_it_verified(self):
        if not BINARY.exists():
            self.skipTest("binary not built; run make build")
        result = run_gate()
        # Coverage must be stated, not implied: a gate that verifies less than it
        # claims is the failure mode this file exists to prevent.
        self.assertIn("command(s)", result.stdout)
        self.assertIn("flag(s) verified", result.stdout)


class GateDetectsDrift(unittest.TestCase):
    def setUp(self):
        if not BINARY.exists():
            self.skipTest("binary not built; run make build")

    def test_renamed_verb_fails(self):
        target = SKILLS / "amq-squad" / "references" / "team-rules-template.md"
        with MutatedSkill(target, "amq-squad doctor", "amq-squad doctorq"):
            result = run_gate()
        self.assertEqual(result.returncode, 1, "a renamed verb must fail the gate")
        self.assertIn("doctorq", result.stderr)

    def test_renamed_verb_with_uppercase_fails_rather_than_vanishing(self):
        """The bug this gate nearly shipped with.

        Tokens failing a lowercase-only verb pattern were skipped, so `evidenceX`
        was DISCARDED instead of flagged: the extracted count dropped and the gate
        exited 0. A drift gate that loses references it cannot parse is worse than
        no gate, because the loss is indistinguishable from agreement.
        """
        target = SKILLS / "cli" / "SKILL.md"
        with MutatedSkill(target, "amq-squad evidence run", "amq-squad evidenceX run"):
            result = run_gate()
        self.assertEqual(result.returncode, 1, "an unparseable verb must fail, not vanish")
        self.assertIn("evidenceX", result.stderr)

    def test_unknown_flag_fails(self):
        target = SKILLS / "amq-squad" / "references" / "pointer-stub-template.md"
        with MutatedSkill(
            target, "amq-squad team sync --apply", "amq-squad team sync --apply --nonexistent-flag"
        ):
            result = run_gate()
        self.assertEqual(result.returncode, 1, "an unknown flag must fail the gate")
        self.assertIn("--nonexistent-flag", result.stderr)


class ExtractionRules(unittest.TestCase):
    def setUp(self):
        self.gate = load_gate()

    def test_prose_mid_sentence_is_not_a_command(self):
        """"This project uses amq-squad for agent team coordination" is English.

        It appears inside a FENCE, because these skills legitimately fence file
        templates as well as shell, so fencing alone cannot distinguish the two.
        The line-start rule does, with no allowlist.
        """
        found = self.gate.extract([SKILLS / "amq-squad" / "references" / "pointer-stub-template.md"])
        self.assertNotIn("for", {verb for verb, _ in found})

    def test_inline_span_wrapping_across_lines_is_extracted(self):
        """cli/SKILL.md names `amq-squad evidence run TASK --profile ...` across a line break.

        A newline-hostile inline pattern misses it SILENTLY, which is the dangerous
        kind of miss: the reference is simply never checked.
        """
        found = self.gate.extract([SKILLS / "cli" / "SKILL.md"])
        self.assertIn(("evidence", "run"), found, "wrapped inline span was not extracted")
        self.assertIn("--profile", found[("evidence", "run")])

    def test_leading_flag_is_not_treated_as_a_verb(self):
        with tempfile.TemporaryDirectory() as tmp:
            probe = Path(tmp) / "probe.md"
            probe.write_text("```\namq-squad --version\n```\n")
            found = self.gate.extract([probe])
        self.assertEqual(found, {}, "`amq-squad --version` names no verb")


class AntiVacuity(unittest.TestCase):
    """The gate must fail loudly rather than pass while checking nothing."""

    def setUp(self):
        self.gate = load_gate()

    def test_help_parser_extracts_a_sane_verb_count(self):
        if not BINARY.exists():
            self.skipTest("binary not built; run make build")
        surface = self.gate.verb_surface(str(BINARY))
        self.assertGreaterEqual(
            len(surface),
            self.gate.MIN_VERBS_IN_SURFACE,
            "the --help parser found too few verbs; a help-format change must fail "
            "loudly rather than empty the surface silently",
        )
        # Spot-check real verbs so a parser that returns plausible garbage fails.
        for verb in ("doctor", "status", "run", "team"):
            self.assertIn(verb, surface)

    def test_floors_are_enforced_not_decorative(self):
        self.assertGreater(self.gate.MIN_COMMANDS, 0)
        self.assertGreater(self.gate.MIN_VERBS_IN_SURFACE, 0)

    def test_gate_fails_when_no_skills_are_found(self):
        """An empty skills tree must be an ERROR, not a pass.

        Asserting a permissive set of exit codes here would be the same vacuity the
        floors exist to prevent, so this pins exit 2 and the message exactly.
        """
        if not BINARY.exists():
            self.skipTest("binary not built; run make build")
        with tempfile.TemporaryDirectory() as empty:
            result = subprocess.run(
                [sys.executable, str(GATE), str(BINARY), empty],
                capture_output=True,
                text=True,
            )
        self.assertEqual(result.returncode, 2, f"empty skills tree must fail:\n{result.stdout}{result.stderr}")
        self.assertIn("no skill sources found", result.stderr)

    def test_gate_fails_when_skills_name_too_few_commands(self):
        """A tree that names almost nothing must trip the floor rather than pass."""
        if not BINARY.exists():
            self.skipTest("binary not built; run make build")
        with tempfile.TemporaryDirectory() as thin:
            (Path(thin) / "thin.md").write_text("```\namq-squad doctor\n```\n")
            result = subprocess.run(
                [sys.executable, str(GATE), str(BINARY), thin],
                capture_output=True,
                text=True,
            )
        self.assertEqual(result.returncode, 2, "a near-empty corpus must trip the anti-vacuity floor")
        self.assertIn("expected >=", result.stderr)


class NotACommandListIsNotStale(unittest.TestCase):
    """Every concession must still have a cause.

    #534's skill rewrite deletes the version-announcement preamble, which is the
    only reason this list is non-empty. When that lands, the entry must be removed,
    and this test is what forces it rather than leaving a permanent exemption
    nobody revisits.
    """

    def test_every_entry_still_appears_in_the_skills(self):
        gate = load_gate()
        corpus = "\n".join(p.read_text() for p in SKILLS.rglob("*.md"))
        for entry, reason in gate.NOT_A_COMMAND.items():
            self.assertIn(
                f"amq-squad {entry}",
                corpus,
                f"'{entry}' is exempted ({reason}) but no longer appears in the skills; "
                "remove the stale entry so the gate stops carrying a concession with no cause",
            )

    def test_entries_carry_a_documented_reason(self):
        gate = load_gate()
        for entry, reason in gate.NOT_A_COMMAND.items():
            self.assertTrue(reason.strip(), f"exemption '{entry}' has no documented reason")


if __name__ == "__main__":
    unittest.main(verbosity=2)
