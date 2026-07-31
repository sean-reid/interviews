# interviews

A platform for running technical interviews that measure resourcefulness, not recall.

Three interview types share one content registry, one variant engine, and one grading
model: live debugging sessions in disposable environments, take-home coding challenges
delivered as clean candidate bundles, and system design exercises reviewed live.
Problems are deliberately too hard to finish; candidates may use any resource,
including AI tools, and the evaluation watches how they work, not how far they get.

## Install

Requires Go 1.24+.

```sh
go install ./cmd/interviews
```

## Usage

```sh
interviews list                       # every problem in the content tree
interviews list --type debugging      # one interview type
interviews describe <problem>         # detail for one problem (interviewer view)
interviews validate                   # check the content tree; exits 1 on any error
```

Problems are parameterized. Resolve a variant for a specific interview by
passing its id as the seed, and pin individual parameters when you need to:

```sh
interviews describe <problem> --seed calm-bison-0731
interviews describe <problem> --seed calm-bison-0731 --set scale=7
```

The same seed always resolves to the same variant, so a session can be
reproduced exactly when grading it later.

Debugging problems run in disposable local environments (kind or docker
compose, per the problem's env spec):

```sh
interviews env up <problem> --seed <id>     # healthy environment
interviews break <problem> --seed <id>      # inject the variant's fault pack
interviews fault status <problem> --seed <id>
interviews fault fix <problem> [fault] --seed <id>
interviews env down <problem> --seed <id>
interviews prove <problem>                  # CI gate: every fault breaks, every fix works
```

Take-homes ship as clean candidate bundles: the candidate-visible files
rendered for the variant, a fresh one-commit git history, and a leak gate
that fails the whole bundle if anything interviewer-only would leave:

```sh
interviews bundle <problem> --seed <id> -o bundle-dir   # or -o drop.tar.gz
```

## Content

Problems live under `content/<type>/<problem>/`, each with a `problem.yaml`
manifest, a `candidate/` tree, and an `interviewer/` tree. Visibility is
fail-closed: a file reaches candidates only if the manifest's `visibility`
globs name it, and nothing under `interviewer/` can be exposed at all.

Grading is rubric-first: a shared resourcefulness rubric (problem
decomposition, evidence over guessing, tool and AI wrangling, adaptation,
communication) with per-level calibration bands grades every interview type;
objective completion is recorded but secondary. AI use is expected and scored
on its own dimension.

```sh
interviews grade sheet <problem> --seed <id> -o sheet.md
interviews grade score <problem> --seed <id>    # fill the objective table from the live env
interviews grade hint <problem> "text" --seed <id> --minute 17
```

Problems get easier as models improve, so calibration is a command. The
red-team harness drives a frontier agent at a problem with exactly what a
candidate gets, scores it with the same fault checks, and records the verdict.
It drives the local `claude` CLI by default, so no API key is needed:

```sh
interviews redteam <problem>                  # calibrate every pack
interviews redteam <problem> --driver api     # via the Messages API instead
interviews redteam ledger --stale             # problems that no longer discriminate
```

A problem an unassisted agent mostly solves is flagged for rework. Re-run the
calibration whenever a stronger model ships.
