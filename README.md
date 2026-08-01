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

## Browsing content

```sh
interviews list                       # every problem in the content tree
interviews list --type debugging      # one interview type
interviews describe <problem>         # detail for one problem (interviewer view)
interviews validate                   # check the content tree; exits 1 on any error
```

Problems are parameterized. Resolve a variant for a specific interview by passing
its id as the seed, and pin individual parameters when you need to:

```sh
interviews describe <problem> --seed calm-bison-0731
interviews describe <problem> --seed calm-bison-0731 --set scale=7
```

The same seed always resolves to the same variant, so a session can be reproduced
exactly when grading it later, and no two candidates get a byte-identical problem.

## Running an interview

### Debugging

Running one is three commands. `start` invents the interview id, builds the
environment, injects the variant's fault pack, and opens the recorded terminal;
everything afterwards defaults to that session, so the id is never typed:

```sh
interviews start <problem> --level senior   # prints the id and both URLs
interviews hint "asked what the events say" # minute measured from the start
interviews end                              # stop, keep the evidence, tear down
```

`interviews sessions` lists what is running and what ended, with the state read
from each environment rather than from the record. `interviews sessions show`
prints one session in full, for when the URLs have been lost.

Underneath, each step is a command of its own, which is what content authoring
and CI use:

```sh
interviews env up <problem> --seed <id>     # healthy environment
interviews break <problem> --seed <id>      # inject the variant's fault pack
interviews fault status <problem> --seed <id>
interviews fault fix <problem> [fault] --seed <id>
interviews env down <problem> --seed <id>
```

`break` and `fault status` print the fault pack, so they are interviewer-only
output. `env down` keeps the session evidence and prints where it is; add
`--purge` to delete that too.

`interviews prove <problem>` is the gate that keeps a scenario honest: on a healthy
environment every fault must break its check when injected and converge after its
documented fix, then the whole pack must break and recover end to end. CI runs it on
every change that could affect a scenario.

The session stack `start` puts up is a shared recorded terminal over that
environment, writable for the candidate and read-only for the observer. Its
pieces are also separate commands:

```sh
interviews session start <problem> --seed <id>   # prints candidate and observer URLs
interviews session timeline <problem> --seed <id> --for 70m   # own window: it samples until it ends
interviews session evidence <problem> --seed <id> --final
interviews session stop <problem> --seed <id>
```

Evidence lands in the session workdir, which every one of these commands
prints. Copy that directory somewhere durable before tearing the environment
down; the grading sheet reads it afterwards.

The two terminal endpoints bind fixed loopback ports, 8001 and 8002, which the
fronting proxy routes by number, so one host runs one live session at a time.
`session start` refuses a second one and waits for both endpoints to answer
before printing any URL, rather than handing you a link nothing serves.

Locally that is one account, your own: the candidate's terminal can read this
checkout, answer keys included, so local mode is for authoring and rehearsal.
On a provisioned host `--candidate-user` puts
the tmux server on a second account that cannot read the content tree, so the
candidate's terminal has no route to the answer keys.

See [the runbook](docs/runbook.md) for provisioning a disposable host on any AWS
account, and for the flow around a live session.

### Take-home

Take-homes ship as clean candidate bundles: the candidate-visible files rendered for
the variant, a fresh one-commit git history, and a leak gate that fails the whole
bundle if anything interviewer-only would leave:

```sh
interviews bundle <problem> --seed <id> -o bundle-dir   # or -o drop.tar.gz
```

### System design

Design problems are offline work followed by a live review. The candidate gets a brief
and a constraint sheet whose requirements contradict each other on purpose. The
interviewer gets a probe pack, reference notes covering several designs that all pass,
and scripted curveballs to introduce mid-review, because a design that only survives
its original assumptions is the thing worth finding out about.

The candidate's half is delivered the same way, through the same leak gate. The drop
carries no git history, because the deliverable is a document rather than a repository:

```sh
interviews bundle <problem> --seed <id> -o design-dir    # or -o design.tar.gz
```

## Content

Problems live under `content/<type>/<problem>/`, each with a `problem.yaml` manifest,
a `candidate/` tree, and an `interviewer/` tree. Visibility is fail-closed: a file
reaches candidates only if the manifest's `visibility` globs name it, and nothing
under `interviewer/` or `faults/` can be exposed at all. Symlinks never count as
candidate-visible, since a link at a candidate path can point at an answer key.

## Grading

Grading is rubric-first: a shared resourcefulness rubric (problem decomposition,
evidence over guessing, tool and AI wrangling, adaptation, communication) with
per-level calibration bands from entry to principal grades every interview type.
Objective completion is recorded but secondary, because nobody is expected to finish.
AI use is expected and scored on its own dimension.

```sh
interviews grade sheet <problem> --seed <id> -o sheet.md
interviews grade score <problem> --seed <id>    # fill the objective table from the live env
interviews grade hint <problem> "text" --seed <id> --minute 17 [--workdir <dir>]
```

`interviews hint` is the one to use during a session: it takes neither a seed nor
a minute. `grade hint` is for logging one against a session this machine did not
start, which is the remote case. Without `--workdir` it refuses a seed with no
session, because a typo there is otherwise invisible.
Hints are logged from wherever the interviewer is sitting, which for a remote session is
not where the evidence lands, and naming a directory creates it. `grade sheet --hints <dir>` merges that ledger into the
sheet, so a session host never needs to be reachable to record one.

## Calibration

Problems get easier as models improve, so calibration is a command. The red-team
harness drives a frontier agent at a problem with exactly what a candidate gets,
scores it with the same fault checks, and records the verdict. It drives the local
`claude` CLI by default, so no API key is needed:

```sh
interviews redteam <problem>                  # calibrate every pack
interviews redteam <problem> --driver api     # via the Messages API instead
interviews redteam ledger --stale             # problems that no longer discriminate
```

A problem an unassisted agent mostly solves is flagged for rework. Re-run the
calibration whenever a stronger model ships.
