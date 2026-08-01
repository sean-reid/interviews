#!/bin/sh
# The selector has to match running pods, which is the mechanism: a selector
# that reads plausibly but matches nothing leaves the service with nowhere to
# send traffic, and the pods stay healthy throughout.
set -eu
selector=$(kubectl -n "$IV_NAMESPACE" get service cache -o jsonpath='{.spec.selector.app}')
[ "$selector" = "cache" ]
matched=$(kubectl -n "$IV_NAMESPACE" get pods -l "app=$selector" \
  --field-selector=status.phase=Running -o name | grep -c .)
[ "$matched" -ge 1 ]
