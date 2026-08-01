# Live review probes

- You wrote that every subscriber receives it exactly once. Show me where an
  acknowledgement can be lost, and what happens then.
- Who deduplicates, you or the subscriber? What did you assume about them?
- How do you know the fanout finished, as opposed to mostly finished?
- Which number in your design would you have to change first if the deadline
  halved?
- What did an AI assistant give you here that you kept, and what did you throw
  away?
