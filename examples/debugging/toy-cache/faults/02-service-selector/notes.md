# 02-service-selector

The service selects `app: cache-v2`; the pods are labelled `app: cache`. The
pod is healthy, so anyone who only looks at pods finds nothing wrong.

What to watch for: whether they think to ask what the service is actually
pointing at. `kubectl get endpoints cache` shows it empty, which is the
observation that discriminates.

Nudge: "the pod is fine. What is in front of it?"
