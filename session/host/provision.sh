#!/usr/bin/env bash
# Provisions a stock Ubuntu 24.04 host into a disposable interview box.
# Idempotent: safe to re-run. Expects REPO_TARBALL_URL and HOSTNAME_FQDN
# in the environment and /etc/interviews/session.env already written
# (problem, seed, tokens, TTL) by whatever booted the host.
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive

KIND_VERSION=v0.29.0
KUBECTL_VERSION=v1.33.2
TTYD_VERSION=1.7.7

apt-get update
apt-get install -y --no-install-recommends \
  docker.io tmux asciinema caddy curl ca-certificates awscli gettext-base sudo

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
[ -x /usr/local/bin/kind ] ||
  fetch_bin "https://github.com/kubernetes-sigs/kind/releases/download/${KIND_VERSION}/kind-linux-${arch}" /usr/local/bin/kind
[ -x /usr/local/bin/kubectl ] ||
  fetch_bin "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}/kubectl" /usr/local/bin/kubectl
[ -x /usr/local/bin/ttyd ] ||
  fetch_bin "https://github.com/tsl0922/ttyd/releases/download/${TTYD_VERSION}/ttyd.${ttyd_arch}" /usr/local/bin/ttyd

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
HOSTNAME_FQDN="$HOSTNAME_FQDN" envsubst '${HOSTNAME_FQDN} ${IV_CANDIDATE_TOKEN} ${IV_OBSERVER_TOKEN}' \
  </opt/interviews/session/host/Caddyfile.tmpl >/etc/caddy/Caddyfile

systemctl daemon-reload
systemctl enable --now docker
systemctl restart caddy
systemctl enable --now iv-ttl.service
systemctl enable --now iv-session.service
systemctl enable --now iv-timeline.timer iv-evidence-sync.timer
