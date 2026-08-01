# toy-cache

An example, not an interview. It exists so the platform has something to
prove against in CI and so the spec has a worked problem to point at, which
means it breaks the rule every real problem follows: it is finishable, and
quickly.

Two faults, both easy. pack-a is the image tag alone; pack-b adds the service
selector, which is the more interesting one because the pod looks healthy.

If you run this with a person, the only thing worth grading is method: did
they read state before changing it, and did they say what they expected to
see before they looked?
