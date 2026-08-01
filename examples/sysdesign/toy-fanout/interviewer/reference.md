# Reference

Several designs pass. What they share is honesty about the guarantee.

A queue per subscriber with at-least-once delivery and idempotent receipt keyed
on an event id is the common shape. Retention bounded by a stated window, with
whatever falls outside it explicitly dropped. Completion measured as
acknowledged deliveries against the subscriber list at event time, not against
the live list, which changes underneath.

A design that promises exactly once end to end is wrong, and the review is
where that gets found out rather than argued about. A design that says
at-least-once with deduplication at the receiver, and admits it depends on the
subscriber, is stronger than one that hides the dependency.

Weak signals: horizontal scaling offered as an answer to a latency question;
unbounded retention; completion defined as the last send rather than the last
acknowledgement.
