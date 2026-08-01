#!/usr/bin/env bash
# Provisions a stock Ubuntu 24.04 host into a disposable interview box.
# Idempotent: safe to re-run. Expects REPO_TARBALL_URL and HOSTNAME_FQDN
# in the environment and /etc/interviews/session.env already written
# (problem, seed, tokens, TTL) by whatever booted the host.
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive

# prereqs installs everything the host needs before any of it is configured:
# the packages, the AWS CLI, and the pinned binaries. It is a function so CI
# can run this phase alone in a stock container. Nothing here touches systemd
# or the network beyond fetching, which is what makes that possible, and it is
# the phase where a package that does not exist on this release shows up.
prereqs() {
  KIND_VERSION=v0.29.0
  KUBECTL_VERSION=v1.33.2
  TTYD_VERSION=1.7.7

  # A cloud image runs unattended-upgrades on boot and holds the dpkg lock,
  # so wait for it rather than racing it, and say so while waiting.
  apt_opts="-o DPkg::Lock::Timeout=600"
  echo "apt: waiting for any boot-time upgrade to release the lock"
  # shellcheck disable=SC2086
  apt-get $apt_opts update
  # No awscli here: Ubuntu 24.04 has no such package, and it took a host that
  # booted, ran, and never served anything to notice. Version 2 comes from AWS
  # below, which is what they support anyway.
  # docker.io is the engine only: docker compose is a separate plugin package,
  # and a scenario that uses compose fails at env up without it.
  # shellcheck disable=SC2086
  apt-get $apt_opts install -y --no-install-recommends \
    docker.io docker-compose-v2 tmux asciinema caddy curl ca-certificates \
    unzip gettext-base sudo

  arch=$(dpkg --print-architecture)
  case "$arch" in
    amd64) ttyd_arch=x86_64 ;;
    arm64) ttyd_arch=aarch64 ;;
    *)
      echo "unsupported architecture: $arch" >&2
      exit 1
      ;;
  esac

  fetch_bin() { # url dest
    curl -fsSL "$1" -o "$2"
    chmod 0755 "$2"
  }
  # The evidence sync and the tarball fetch both need it, so it has to land
  # before either.
  if ! command -v aws >/dev/null 2>&1; then
    curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-${ttyd_arch}.zip" -o /tmp/awscliv2.zip
    unzip -q -o /tmp/awscliv2.zip -d /tmp
    /tmp/aws/install --update
    rm -rf /tmp/aws /tmp/awscliv2.zip
  fi

  [ -x /usr/local/bin/kind ] ||
    fetch_bin "https://github.com/kubernetes-sigs/kind/releases/download/${KIND_VERSION}/kind-linux-${arch}" /usr/local/bin/kind
  [ -x /usr/local/bin/kubectl ] ||
    fetch_bin "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}/kubectl" /usr/local/bin/kubectl
  [ -x /usr/local/bin/ttyd ] ||
    fetch_bin "https://github.com/tsl0922/ttyd/releases/download/${TTYD_VERSION}/ttyd.${ttyd_arch}" /usr/local/bin/ttyd

  # Each of these has to work, not merely be present. Two releases running,
  # a package that installed cleanly and then could not do its job is how
  # both host bugs reached a candidate-facing box.
  docker --version
  docker compose version
  aws --version
  /usr/local/bin/kind --version
  /usr/local/bin/kubectl version --client=true -o yaml >/dev/null
  /usr/local/bin/ttyd --version
  tmux -V
  asciinema --version
  caddy version
}

# Called with the flag by CI, which stops before anything that needs a real
# host. Provisioning a host runs the whole script with no arguments.
if [ "${1:-}" = "--prereqs-only" ]; then
  prereqs
  echo "prereqs completed"
  exit 0
fi

# Uploaded at milestones as well as on exit, because a provision that is
# merely slow looks identical to one that is stuck when the only report comes
# at the end. Failures are cheap to diagnose; being blind for ten minutes is
# not.
milestone() {
  echo "=== $1 $(date -Is) ==="
  if [ -r /etc/interviews/session.env ] && command -v aws >/dev/null 2>&1; then
    # shellcheck source=/dev/null
    dest=$(. /etc/interviews/session.env && printf '%s' "${IV_EVIDENCE_S3:-}")
    [ -n "$dest" ] && aws s3 cp /var/log/iv-provision.log "$dest/provision.log" >/dev/null 2>&1 || true
  fi
}

# The CLI is installed before anything else so the log below can be uploaded
# from the very first milestone. curl and python3 are on the base image, which
# is what makes this possible without apt.
bootstrap_aws() {
  command -v aws >/dev/null 2>&1 && return 0
  curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o /tmp/awscliv2.zip
  python3 -m zipfile -e /tmp/awscliv2.zip /tmp/
  chmod +x /tmp/aws/install /tmp/aws/dist/aws
  /tmp/aws/install --update
  rm -rf /tmp/aws /tmp/awscliv2.zip
}

# Everything from here is logged, and the log is uploaded on the way out
# whether this succeeds or fails. A host has no ssh, no key pair, and no SSM
# by design, so without this a failed provision is a black box.
mkdir -p /var/log
exec > >(tee -a /var/log/iv-provision.log) 2>&1
echo "provisioning started $(date -Is)"

bootstrap_aws || echo "could not install the aws cli early: milestones will start late"
milestone "provisioning started, installing prerequisites"

prereqs
milestone "prereqs done"

# Armed after prereqs because it needs the AWS CLI. A failure inside prereqs
# is the case CI covers instead.
upload_log() {
  status=$?
  echo "provisioning finished $(date -Is) with status $status"
  if [ -r /etc/interviews/session.env ]; then
    # shellcheck source=/dev/null
    evidence=$(. /etc/interviews/session.env && printf '%s' "${IV_EVIDENCE_S3:-}")
    if [ -n "$evidence" ]; then
      aws s3 cp /var/log/iv-provision.log "$evidence/provision.log" >/dev/null 2>&1 ||
        echo "could not upload the provisioning log"
    fi
  fi
  return $status
}
trap upload_log EXIT

# interviewer runs the platform and owns the content; candidate owns the
# tmux server the browser terminal attaches to, and nothing else: no
# docker group, no sudo, no read on /opt/interviews.
id interviewer &>/dev/null || useradd --create-home --shell /bin/bash interviewer
usermod -aG docker interviewer
id candidate &>/dev/null || useradd --create-home --shell /bin/bash candidate

# The two accounts meet in one group, which is what lets the interviewer's
# recorder and observer attach to the candidate's tmux server.
getent group iv-session >/dev/null || groupadd --system iv-session
usermod -aG iv-session interviewer
usermod -aG iv-session candidate

# Setgid on the socket directory puts the socket the candidate's tmux
# creates into the shared group; the session then widens it to 0660.
install -d -o root -g root -m 0755 /run/interviews
install -d -o root -g iv-session -m 2770 /run/interviews/tmux
install -d -o interviewer -g iv-session -m 2750 /run/interviews/kube

mkdir -p /opt/interviews
case "$REPO_TARBALL_URL" in
  s3://*) aws s3 cp "$REPO_TARBALL_URL" /tmp/interviews.tar.gz ;;
  *) curl -fsSL "$REPO_TARBALL_URL" -o /tmp/interviews.tar.gz ;;
esac
tar -xzf /tmp/interviews.tar.gz -C /opt/interviews
rm -f /tmp/interviews.tar.gz
chown -R interviewer:interviewer /opt/interviews
chmod 0700 /opt/interviews

# The answer keys live in there, so an interview on a host where the
# candidate can walk into the tree is not worth running.
if sudo -u candidate test -x /opt/interviews; then
  echo "candidate can reach /opt/interviews; refusing to provision" >&2
  exit 1
fi

install -m 0644 /opt/interviews/session/host/iv-*.service /opt/interviews/session/host/iv-*.timer /etc/systemd/system/

# Starting the candidate's tmux server is the one thing the interviewer
# does as the candidate; both ttyd processes and the recorder stay with
# the interviewer, which is what keeps the recording out of reach.
install -m 0440 /opt/interviews/session/host/iv-session.sudoers /etc/sudoers.d/iv-session
visudo -cqf /etc/sudoers.d/iv-session

# Caddy fronts both ttyd ports with TLS on the sslip.io hostname; the
# secret path tokens are the only routes in.
set -a
# shellcheck source=/dev/null
. /etc/interviews/session.env
set +a
HOSTNAME_FQDN="$HOSTNAME_FQDN" envsubst '${HOSTNAME_FQDN} ${IV_CANDIDATE_TOKEN} ${IV_OBSERVER_TOKEN} ${IV_APP_TOKEN}' \
  </opt/interviews/session/host/Caddyfile.tmpl >/etc/caddy/Caddyfile

systemctl daemon-reload
systemctl enable --now docker
systemctl restart caddy
milestone "caddy restarted"
systemctl enable --now iv-ttl.service
# The session is the interview, so its failure is the provision's failure. It
# gets started without set -e killing the script first, because the whole
# point is to capture why before anything exits: there is no ssh to come back
# with, and the log below is uploaded either way.
session_started=1
milestone "starting the session, which builds the environment"
systemctl enable iv-session.service
systemctl start iv-session.service || session_started=0
if [ "$session_started" = 0 ]; then
  echo "=== iv-session.service did not start ==="
  systemctl status --no-pager --full iv-session.service 2>&1 || true
  echo "=== journal ==="
  journalctl --no-pager --lines=100 -u iv-session.service 2>&1 || true
  echo "=== end of session diagnostics ==="
fi

systemctl enable --now iv-timeline.timer iv-evidence-sync.timer

# Reported last so the log carries everything above it. A host serving nothing
# is not a provisioned host, and saying so is what stops it reading as ready.
if [ "$session_started" = 0 ]; then
  echo "provisioning reached the end but the session never started"
  exit 1
fi
