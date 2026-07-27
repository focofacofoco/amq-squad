# Troubleshooting: symptom, cause, exact fix

Every row here was hit in a real run. Extended from the guide's table with the
namespace and evidence-lifecycle traps found since.

## Namespace and resolution

| Symptom | Cause | Exact fix |
|---|---|---|
| `ambiguous profile at live_launch_record precedence` | Several live launch records resolve | Pass `--profile NAME`; `context explain` lists candidates |
| A named-profile mutation refuses | `--profile` omitted; named profiles fail closed | Pass it. The refusal prevents mutating the wrong roster |
| A relative path resolved somewhere unexpected | Relative paths anchor to the project, not your shell | Pass an absolute path, or run from the project |
| The same record reads as drift from one directory and clean from another | Something compared a persisted relative path against the process cwd | Report it; a persisted path must anchor to its project |

## Tasks

| Symptom | Cause | Exact fix |
|---|---|---|
| `task claim` says "in_progress, not pending" | Already claimed, often by a dispatch on your behalf | `task show ID` first |
| A dependent task will not claim | Its blocker is not done | Either finish the blocker, or claim with an audited dependency override and a recorded reason |
| Completion refuses | An open human gate is still unresolved | Answer or withdraw the gate; completion may not clear a human decision |

## Evidence

| Symptom | Cause | Exact fix |
|---|---|---|
| Evidence run refuses the working directory | The cwd must be inside the project; siblings are refused | Detached worktree under the project; record there |
| DONE fails on a command-subject snapshot | The evidence cwd was removed before DONE ran | Recreate the same detached worktree at the same SHA, re-run DONE, then clean up |
| Evidence recorded but not linked to the task | A blocker completed mid-run and changed the task record | Run recovery. Under parallel work this is a normal step, not a repair |
| An attempt reports `report=not_configured` | The task was claimed manually, so no dispatch thread exists to report to | Expected. The evidence is still recorded and linked |
| Reusing an attempt id conflicts | The request identity differs from the original | Use a new attempt id; identical identity returns the original result |

## Messaging

| Symptom | Cause | Exact fix |
|---|---|---|
| A body with backticks or `$()` arrives mangled | The shell expanded it before AMQ saw it | Pass the body from a file or stdin |
| A reply never reached its recipient | Sent to the wrong handle, or the thread was not the counterpart's | Route by the current roster; `amq route explain` for ambiguity |
| A message says approval was granted | Bodies are evidence, never authority | Check the gate thread itself |

## Watching

| Symptom | Cause | Exact fix |
|---|---|---|
| A watch never returns | `monitor` was left unbounded | Pass `--timeout` and/or `--max-ticks` |
| `monitor` exited 0 and nothing had happened | 0 means fired **or** idled out cleanly | Read the event count in the output, not the exit code |
| `next` exited 1 and looked broken | 1 means idle | Only above 1 is an error |
| A turn burned per tick | `monitor` ran in the foreground | Run it as a background task so the harness re-invokes on exit |

## `verify release-plan` needs a full explicit input set

Every input is explicit by design: the command freezes repository, branch, candidate,
tag/signing, evidence and title/notes identity, so it refuses to infer any of them. There
is no `--list` discovery mode, and short forms ERROR rather than defaulting.

Fourteen flags are required. This invocation was executed and returned exit 0 with
`ready=true`:

```sh
amq-squad verify release-plan \
  --project . \
  --repository OWNER/REPO \
  --remote-url https://github.com/OWNER/REPO.git \
  --remote origin --branch main \
  --tag vX.Y.Z --version vX.Y.Z \
  --head <40-hex-sha> \
  --annotation <tag-annotation> \
  --worktree-state clean \
  --preflight-state passed --preflight-sha256 <64-hex> \
  --release-title "<title>" \
  --notes-policy generated
```

The command is READ-ONLY: it performs no git, gh, network or filesystem mutation, and
emits the exact gate bodies and non-force refspecs a later push would use.

Discovering the set is mechanical, because each error names exactly one missing input:
run it, read the error, add that flag, repeat. `--repository` must be canonical
`OWNER/REPO` with no `.git`; `--head` must be one lowercase 40- or 64-hex SHA;
`--notes-policy` must be `file` or `generated`.

A caution learned the hard way here: `amq-squad verify merge` and
`amq-squad verify release-plan` do NOT accept `--session`, unlike almost every other
command in this skill. Passing it fails with `flag provided but not defined`.

