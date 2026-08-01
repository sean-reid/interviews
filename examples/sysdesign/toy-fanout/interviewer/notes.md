# toy-fanout

An example of the shape. It is smaller than a real design problem and its
requirements only nearly contradict, where a real one's do.

The tension worth reaching: exactly once across an unreliable network does not
exist, so the honest design is at-least-once delivery with idempotent receipt,
and the interesting question is who owns the deduplication. A candidate who
promises exactly once end to end has not noticed.
