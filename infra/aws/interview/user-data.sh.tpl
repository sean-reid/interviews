#!/usr/bin/env bash
set -euo pipefail

mkdir -p /etc/interviews
cat >/etc/interviews/session.env <<'EOF'
IV_PROBLEM=${problem}
IV_SEED=${seed}
IV_CANDIDATE_TOKEN=${candidate_token}
IV_OBSERVER_TOKEN=${observer_token}
IV_TTL_MINUTES=${ttl_minutes}
IV_HOSTNAME=${hostname}
IV_EVIDENCE_S3=s3://${evidence_bucket}/${seed}
EOF
chmod 0600 /etc/interviews/session.env

export REPO_TARBALL_URL='${repo_tarball_s3_uri}'
export HOSTNAME_FQDN='${hostname}'

# provision.sh follows, appended verbatim by the instance's user_data.
