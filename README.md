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
interviews list                 # available problems across all types
interviews describe <problem>   # detail for one problem (interviewer view)
```

More commands (validate, prove, bundle, score, redteam) land as the platform grows.
