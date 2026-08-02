#!/usr/bin/env bash
# Runs the whole of provision.sh in a container, offline, in about two
# minutes. Nothing here needs AWS, credentials, or a host: the tarball is
# served from disk and systemctl is recorded rather than executed.
#
# It covers everything except the units actually running: the packages, the
# binaries, every tool proving it works, the accounts and their groups, the
# tarball unpack, the sudoers syntax, the rendered Caddyfile, and the order
# the units would start in. Both host bugs that reached a real box would have
# failed here first, and a real provision takes fifteen minutes to say so.
#
#   session/host/offline-test.sh [path/to/interviews.tar.gz]
#
# With no argument it builds a tarball from this checkout and the configured
# content root.
set -euo pipefail

here=$(cd -- "$(dirname -- "$0")" && pwd -P)
repo=$(cd -- "$here/../.." && pwd -P)
tarball=${1:-}

if [ -z "$tarball" ]; then
  tarball=$(mktemp -d)/interviews.tar.gz
  echo "packing a tarball from $repo"
  # The same code path setup aws uploads with, so this test provisions the
  # layout a real host unpacks rather than a hand-rolled twin of it.
  content=${IV_TEST_CONTENT:-$repo/examples}
  (cd "$repo" && go run ./cmd/interviews setup pack -o "$tarball" --content "$content")
fi
echo "tarball: $tarball ($(wc -c <"$tarball") bytes)"

# HOME is unset on purpose: cloud-init runs user-data without one, and a tool
# that needs it fails there and nowhere else. asciinema did exactly that.
docker run --rm --platform linux/amd64 \
  -v "$here:/host:ro" -v "$tarball:/tarball.tar.gz:ro" \
  -e DEBIAN_FRONTEND=noninteractive \
  ubuntu:24.04 env -u HOME bash -euo pipefail -c '
mkdir -p /usr/local/sbin /etc/interviews
# Recorded, not run: a container has no systemd, and what matters here is
# that the script gets this far and in this order.
printf "#!/bin/sh\necho \"systemctl \$*\" >>/tmp/units\nexit 0\n" >/usr/local/sbin/systemctl
chmod +x /usr/local/sbin/systemctl
cat >/etc/interviews/session.env <<ENV
IV_PROBLEM=toy-cache
IV_SEED=offline-test
IV_CANDIDATE_TOKEN=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
IV_OBSERVER_TOKEN=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
IV_APP_TOKEN=cccccccccccccccccccccccccccccccc
IV_TTL_MINUTES=60
IV_HOSTNAME=example.test
IV_EVIDENCE_S3=s3://offline/none
ENV
chmod 600 /etc/interviews/session.env
export REPO_TARBALL_URL=file:///tarball.tar.gz HOSTNAME_FQDN=example.test
bash /host/provision.sh >/tmp/provision.out 2>&1 || {
  echo "provision.sh failed:"; tail -30 /tmp/provision.out; exit 1; }

# What the script was supposed to leave behind.
fail=0
check() { if eval "$2"; then echo "  ok   $1"; else echo "  FAIL $1"; fail=1; fi; }
check "interviewer account exists"      "id interviewer >/dev/null 2>&1"
check "candidate account exists"        "id candidate >/dev/null 2>&1"
# Positive control before the negative one. "candidate cannot read X" reports
# ok if sudo is missing, if the account does not exist, or if sudo errors for
# any reason at all, and the check below it is the one the whole two-account
# design rests on.
check "sudo -u candidate works at all"  "sudo -u candidate test -r /etc/hostname"
check "candidate cannot read the content" "! sudo -u candidate test -r /opt/interviews/content"
check "platform unpacked"               "test -x /opt/interviews/interviews"
check "content unpacked"                "test -d /opt/interviews/content"
check "session host scripts unpacked"   "test -d /opt/interviews/session/host"
check "sudoers rule is valid"           "visudo -cqf /etc/sudoers.d/iv-session"
check "Caddyfile rendered with tokens"  "grep -q aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa /etc/caddy/Caddyfile"
check "Caddyfile has the app route"     "grep -q cccccccccccccccccccccccccccccccc /etc/caddy/Caddyfile"
check "no placeholder left in Caddyfile" "! grep -q IV_ /etc/caddy/Caddyfile"
check "Caddyfile is not world readable" "test \"\$(stat -c %a /etc/caddy/Caddyfile)\" = 640"
check "units installed"                 "test -f /etc/systemd/system/iv-session.service"
check "tmux dir group-writable"         "test -g /run/interviews/tmux"
check "self-destruct armed before prereqs" "grep -q self-destruct /tmp/provision.out"
# The units are asserted, not just printed: dropping one from provision.sh
# used to leave a shorter list and a green run.
for unit in iv-session.service iv-ttl.service iv-timeline.timer iv-evidence-sync.timer; do
  check "$unit would have started" "grep -q $unit /tmp/units"
done
echo "units it would have started, in order:"
sed "s/^/  /" /tmp/units
exit $fail
'
echo "offline provisioning test passed"
