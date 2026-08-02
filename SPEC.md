# Authoring problems

Everything the tool needs to run a problem it has never seen. Write a problem
against this and `interviews validate` will tell you what is wrong before a
candidate does.

The design goal behind every rule here: measure resourcefulness, not recall. A
problem that a strong candidate finishes comfortably measures speed. A problem
whose answer can be recalled measures memory. Problems are deliberately larger
than their time budget, candidates may use any resource including AI tools, and
what gets graded is how they worked.

## Layout

```
content/
  debugging/<problem>/
  takehome/<problem>/
  sysdesign/<problem>/
```

The directory under `content/` is the interview type, and only those three
names are allowed. Each problem directory holds:

```
problem.yaml        the manifest, always
candidate/          what the candidate receives
interviewer/        notes, probes, reference answers; never delivered
```

Every type requires `candidate/brief.md`: it is the exact text the candidate
starts from. Each type then requires its own files on top, and `validate`
refuses a problem missing any of them:

| Type | Also required |
|---|---|
| debugging | `env.yaml`, `faults/` with at least one fault, `env/` verify script |
| takehome | `interviewer/probes.md`; the brief must tell the candidate to write the stopping-point writeup |
| sysdesign | `candidate/brief.md` pointing at `candidate/constraints.md`, `review.yaml`, `interviewer/probes.md`, `interviewer/reference.md` |

The delivered bundle opens with a generated ABOUT.md front page; the brief is
yours, the front page is not. A take-home that ships a `harness/` directory
gets a front-page line pointing the candidate at it, so put runner tooling
there.

The offline types demand a probe pack because the live review is where they are
scored: a document read alone tells you what someone can write, and the review
tells you whether they understand it.

## Visibility is fail-closed

A file reaches a candidate only if a glob in `visibility.candidate` names it.
Everything else is interviewer-only by default, so forgetting to hide something
is not a failure mode; forgetting to expose it is, and it shows up as an empty
bundle rather than a leak.

On top of that, `interviewer/` and `faults/` can never be exposed, at any depth,
and neither can `problem.yaml`, `env.yaml`, or `review.yaml`, whatever the
globs say. The check is case-insensitive because a case-insensitive filesystem
would otherwise let `Interviewer/` through. Symlinks
and hard links never count as candidate-visible: a link at a candidate path can
point at an answer key.

```yaml
visibility:
  candidate:
    - candidate/**
```

## problem.yaml

```yaml
schema: 1
id: toy-cache                 # unique across the content root, matches the directory
type: debugging               # debugging | takehome | sysdesign
title: The toy cache does not answer
summary: One line a candidate sees in a listing.
disciplines: [systems, infra] # systems, infra, data-eng, ai-ml, comp-bio, physics, stats, signals
levels: [mid, senior]         # entry, mid, senior, staff, principal
flavor: kubernetes            # debugging only: kubernetes, compose-linux
class: legacy-rescue          # takehome only: constrained-systems, legacy-rescue,
                              # underspecified-product, optimization-ladder
time:
  session_minutes: 60         # debugging
  soft_budget_hours: 5        # takehome and sysdesign
params:
  fault_pack:
    type: choice
    of: [pack-a, pack-b]
  scale:
    type: int
    min: 3
    max: 40
  team_name:
    type: string
    default: umbrella
  db_password:
    type: choice
    of: [drop-nine, spool-seven]
    secret: true              # kept out of grading artifacts
visibility:
  candidate: [candidate/**]
```

`levels` is what the problem claims to grade, and the sheet prints the
calibration band for the level an interview was started with.

A parameter with a `default` is pinned to it: it is never drawn, and only an
explicit override changes it. String parameters must declare one, since there
is nothing sensible to draw a string from.

## Variants

Every parameter resolves from `SHA-256(problem | interview id | parameter
name)`, so the same seed always draws the same environment and two candidates
draw different ones. Parameters have their own derivation streams, which means
adding a parameter never shifts the values of the ones already there: an
existing seed keeps resolving to the same problem it did last week.

Candidate-visible text is a Go template rendered with the resolved parameters,
so `{{.team_name}}` in a brief becomes the drawn value. Referencing an
undeclared parameter is a validation error rather than an empty string.

Only known text extensions and extensionless files are rendered; the list
lives in `variant.IsTemplated`. Anything else ships byte for byte, so a
template hole in, say, a `.png` name is never substituted and fails the
bundle's leak gate instead of shipping half-rendered.

Debugging problems have a rule of their own: 500 seeds must resolve to at least
450 distinct environments. A problem that varies in nothing but its fault pack
is one leaked write-up away from useless, and `validate` refuses it.

## env.yaml, debugging only

```yaml
provider: kind                # kind | compose, and it must suit the flavor
kind:
  manifests: env/manifests    # applied after template rendering
  namespace: toy-{{.app_name}}
  node_image: ""              # only if the problem needs a Kubernetes of its own
  build:                      # images built from this tree, no registry needed
    - image: my-api
      context: app/api
compose:
  file: env/docker-compose.yml
  configs: env/configs        # optional, rendered alongside
verify: env/verify.sh         # exit 0 if and only if the app works end to end
app:                          # optional: what a candidate can open in a browser
  service: cache              # kind only
  port: "80"                  # published host port on compose; a template is allowed
  path: /
```

The platform labels the kind namespace `pod-security.kubernetes.io/enforce:
baseline` itself when the environment comes up, so containment never depends
on a manifest remembering it; a namespace manifest may still declare the same
labels.

Environment files may reference two builtins on top of the declared
parameters: `{{._dir}}`, the problem directory on disk, for absolute build
contexts in compose files, and `{{._workdir}}`, the session's state
directory. Candidate-visible files get neither.

`verify` defines what healthy means for the whole scenario, and it is the thing
`prove` uses to decide the pack broke the app. A verify that cannot fail makes
every fault look fixed, so `prove` checks that too.

## The fault contract

One directory per fault under `faults/`, and the directory name is the fault id.

```
faults/01-image-tag/
  fault.yaml
  inject.sh     make it broken
  check.sh      exit 0 fixed, 1 present, 2 the check itself could not run
  fix.sh        the answer key
  notes.md      what the candidate should discover, and how to nudge
```

```yaml
id: 01-image-tag
title: Cache image tag is mistyped
tier: easy                    # easy | medium | hard
packs: [pack-a, pack-b]       # which fault_pack values include it
masks: [02-service-selector]  # faults whose symptoms this one hides
settle_seconds: 5             # grace before the broken check, for slow symptoms
fix_timeout_seconds: 120      # bound on convergence polling after fix.sh
```

Every script, `verify` included, must carry the executable bit; `validate`
refuses one that does not.

Scripts receive `IV_PROBLEM`, `IV_SEED`, `IV_ENV`, `IV_WORKDIR`, the
provider's variables, and every parameter as `IV_PARAM_<NAME>`. On kind the
provider sets `IV_NAMESPACE`, `IV_CLUSTER`, and `KUBECONFIG`; on compose,
`IV_COMPOSE_FILE` and `IV_PROJECT`. Read values from those rather than
hardcoding them, or the fault only works for one variant.

Two rules cost real debugging time when broken:

**Fault ids sort into injection order.** Faults are injected in lexical order,
so a fault that takes a dependency down has to sort before one that needs it up.
The numeric prefix is load-bearing.

**A check must test its mechanism, not end-to-end health.** If `check.sh` just
calls the health endpoint, it cannot tell a real fix from an injection that
never took effect, and it will report fixed while the fault is still there.
Check the thing the fault changed.

Exit 2 exists because a check that cannot run says nothing about its fault.
Reporting that as "still broken" hides a broken script for a whole interview,
and the grading sheet distinguishes the two.

## review.yaml, sysdesign only

```yaml
tensions:                     # requirements that contradict on purpose
  - id: cost-vs-freshness
    summary: What the candidate has to trade off.
    hides_in: Where in the brief the conflict is buried.
    good_move: What noticing it looks like.
curveballs:                   # introduced mid-review
  - id: region-doubles
    prompt: What you tell them.
    probes: What to ask next.
    good_move: What a strong answer does.
    red_flag: What a weak one does.
  - id: budget-halves
    prompt: What you tell them.
    probes: What to ask next.
    good_move: What a strong answer does.
    red_flag: What a weak one does.
```

At least one tension and at least two curveballs are required, so a review
can escalate past the first change. A design that only survives its original
assumptions is the thing worth finding out about, which is what the
curveballs are for.

## Worked examples

`examples/` holds one problem per type: `toy-cache` (debugging, kubernetes,
two faults), `toy-dedupe` (take-home), and `toy-fanout` (system design). They
are proven in CI, so they are always current, and they break the one rule every
real problem follows: they are small and finishable. Read them for shape, not
for difficulty.

```sh
interviews list --content examples
interviews prove toy-cache --content examples
```

## Checking your work

```sh
interviews validate --content <root>     # schema, visibility, templates, spread
interviews describe <problem> --seed s   # what a variant resolves to
interviews bundle <problem> --seed s -o /tmp/drop   # what the candidate gets
interviews prove <problem>               # debugging: the full anti-rot cycle
```

`prove` is the gate worth running before a problem meets anyone: it brings the
environment up healthy, then for every fault injects it, confirms the check goes
broken, applies the documented fix, and confirms the check goes fixed; then it
injects the whole pack, confirms end-to-end verify fails, fixes everything, and
confirms verify passes. A fault whose fix does not work, or whose check cannot
tell, fails the gate rather than the interview.
