# toy-dedupe

An example of the shape, not a real take-home: two hours and genuinely
finishable, where a real one is deliberately not.

The ladder, roughly: a set of every key seen (correct, unbounded); a set with
time-based eviction (correct within the window, bounded); a bounded structure
that admits false positives and says so. The interesting answer is the third
with the error rate stated, or the second with the memory ceiling computed.

At review, the question worth asking is what happens when the window
assumption is wrong. Anyone who claims exactness while evicting has not
noticed the contradiction, and finding that out is the point.
