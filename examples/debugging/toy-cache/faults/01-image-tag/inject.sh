#!/bin/sh
set -eu
kubectl -n "$IV_NAMESPACE" set image deployment/cache cache=nginx:1.27-alpne
