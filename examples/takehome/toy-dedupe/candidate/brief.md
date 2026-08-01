# Deduplicate a stream you cannot hold

Records arrive as one JSON object per line on stdin. Duplicates are common and
always arrive within {{.window_seconds}} seconds of the original. Two records
are the same record when their `{{.key_field}}` matches.

Write a program that reads stdin and writes each record once to stdout, in the
order it first appeared.

The constraint that makes this a problem: the stream does not fit in memory,
and neither does {{.window_seconds}} seconds of it at peak. You will have to
decide what to keep, what to drop, and what to be wrong about. Say which.

## What to send back

Your program, a short README covering how to run it and what you traded away,
and a STOPPING-POINT.md saying where you stopped and what you would do next.

This is an example problem shipped with the platform, so it is smaller than a
real take-home. A real one is deliberately too large to finish.
