# Constraints

- {{.subscriber_count}} subscribers, each an HTTP endpoint you POST to.
- Fanout completes within {{.fanout_sla_seconds}} seconds of the event.
- Every subscriber receives the event. No subscriber receives it twice.
- Subscribers may be down for hours and return. Some acknowledge slowly. Some
  accept a delivery and then time out before acknowledging.
- You may not ask subscribers to change anything about their endpoint.
- One region. Assume the network inside it is fast and occasionally lossy.

## What your document has to cover

- Delivery guarantees, stated precisely, including what you cannot guarantee.
- What you store, and for how long.
- How a subscriber that missed a day catches up.
- How you know the fanout finished.
