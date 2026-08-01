#!/bin/sh
set -eu
kubectl -n "$IV_NAMESPACE" patch service cache --type merge \
  -p '{"spec":{"selector":{"app":"cache"}}}'
