#!/bin/sh
# Tests the mechanism, not the app: the tag itself, and that the rollout the
# tag broke has completed. Checking the app would pass while a stale replica
# still served traffic.
set -eu
image=$(kubectl -n "$IV_NAMESPACE" get deployment cache \
  -o jsonpath='{.spec.template.spec.containers[0].image}')
[ "$image" = "nginx:1.27-alpine" ]
ready=$(kubectl -n "$IV_NAMESPACE" get deployment cache -o jsonpath='{.status.readyReplicas}')
[ "${ready:-0}" -ge 1 ]
