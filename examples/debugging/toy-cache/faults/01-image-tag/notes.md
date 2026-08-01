# 01-image-tag

The tag is `alpne`. `kubectl get pods` shows ImagePullBackOff, and
`kubectl describe pod` names the tag in its events.

What to watch for: whether they read the pod state before changing anything.
The fault is trivial, so what it measures is method, not knowledge.

Nudge, if they are staring at the service instead: "what does the pod say
about itself?"
