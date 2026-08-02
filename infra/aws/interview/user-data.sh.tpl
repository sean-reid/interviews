#!/usr/bin/env bash
set -euo pipefail

# The session unit loads this file, so everything in it reaches the commands
# that build the environment. IV_WHERE and IV_CONTENT_VERSION are how the
# host tells them what it is and which content it unpacked; neither is
# knowable from inside the box, and both go into the evidence.
mkdir -p /etc/interviews
cat >/etc/interviews/session.env <<'EOF'
IV_PROBLEM=${problem}
IV_SEED=${seed}
IV_CANDIDATE_TOKEN=${candidate_token}
IV_OBSERVER_TOKEN=${observer_token}
IV_APP_TOKEN=${app_token}
IV_TTL_MINUTES=${ttl_minutes}
IV_HOSTNAME=${hostname}
IV_EVIDENCE_S3=s3://${evidence_bucket}/${seed}
IV_WHERE=host
IV_CONTENT_VERSION=${content_version}
EOF
chmod 0600 /etc/interviews/session.env

export REPO_TARBALL_URL='${repo_tarball_s3_uri}'
export HOSTNAME_FQDN='${hostname}'

# provision.sh follows, appended verbatim by the instance's user_data.
