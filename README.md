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

## Content

Problems live under `content/<type>/<problem>/`, each with a `problem.yaml`
manifest, a `candidate/` tree, and an `interviewer/` tree. Visibility is
fail-closed: a file reaches candidates only if the manifest's `visibility`
globs name it, and nothing under `interviewer/` can be exposed at all.

More commands (prove, bundle, score, redteam) land as the platform grows.
