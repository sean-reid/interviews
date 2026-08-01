#!/bin/sh
# Healthy iff the deployment is rolled out and the service routes to it. The
# request goes through the service name on purpose: reaching the pod directly
# would pass while the service selector is wrong.
set -eu
ns="$IV_NAMESPACE"
kubectl -n "$ns" rollout status deployment/cache --timeout=10s >/dev/null
pod=$(kubectl -n "$ns" get pods -l app=cache \
  --field-selector=status.phase=Running \
  -o jsonpath='{.items[0].metadata.name}')
kubectl -n "$ns" exec "$pod" -- wget -qO- -T 5 http://cache/ | grep -qi "welcome to nginx"
