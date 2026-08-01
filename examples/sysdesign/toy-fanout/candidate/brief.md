# Fan out a notification nobody may miss twice

One event has to reach {{.subscriber_count}} subscribers within
{{.fanout_sla_seconds}} seconds. Every subscriber must receive it. No
subscriber may receive it twice.

Subscribers are an HTTP endpoint each. Some are slow. Some are down for hours
and come back. A few will accept a delivery and then time out before
acknowledging it.

Design the system. Cover delivery guarantees, what you store and for how long,
how a subscriber that has been down for a day catches up, and how you know the
whole fanout finished.

Write a design document with whatever diagrams help. There is nothing to run.

This is an example problem shipped with the platform, so it is smaller than a
real design exercise. A real one has requirements that contradict each other.

The numbers and the rules you have to work inside are in
[constraints.md](constraints.md).
