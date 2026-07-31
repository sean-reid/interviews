# Live interview runbook

One disposable EC2 host per interview: broken environment, shared recorded
terminal, TTL self-destruct. Evidence outlives the host in S3.

## One-time setup

Create the evidence bucket, then build and upload the platform tarball
(the tarball root holds the linux binary as `interviews`, plus `content/`
and `session/host/`):

```sh
cd infra/aws/account
terraform apply -var region=eu-west-1 -var bucket=my-interview-evidence

GOOS=linux GOARCH=amd64 go build -o interviews ./cmd/interviews
tar czf interviews.tar.gz interviews content session/host
aws s3 cp interviews.tar.gz s3://my-interview-evidence/tarballs/interviews.tar.gz
```

## Provision a session

```sh
cd infra/aws/interview
terraform apply \
  -var region=eu-west-1 \
  -var problem=relay \
  -var seed=calm-bison-0731 \
  -var evidence_bucket=my-interview-evidence \
  -var repo_tarball_s3_uri=s3://my-interview-evidence/tarballs/interviews.tar.gz
```

Watch boot until `curl https://$(terraform output -raw public_ip).sslip.io/healthz`
returns ok and the observer URL shows a shell prompt; environment build takes
a few minutes. The URLs are sensitive outputs:

```sh
terraform output -raw observer_url
terraform output -raw candidate_url
```

## During

Open the observer URL yourself: read-only, invisible to the candidate. Send
the candidate URL at start time, not before; the recording and the TTL clock
run from boot.

Log hints as you give them, from your own machine. The host has no key pair,
no port 22, and no SSM, so there is no shell on it to log from. Run this from
a checkout of this repository, once per hint, keeping the same workdir for
the whole session:

```sh
interviews grade hint relay "asked what the health endpoint returns" \
  --seed calm-bison-0731 --minute 9 --workdir ~/interviews/calm-bison-0731
```

## The two accounts

The candidate URL is a shell on the `candidate` account, which owns the tmux
server the browser attaches to. It cannot read `/opt/interviews`, so the
answer keys are out of reach, and on kubernetes problems its kubectl is a
service account scoped to the scenario namespace. Provisioning fails rather
than continuing if the candidate can reach the content tree.

The `interviewer` account runs the platform, the recorder, and both ttyd
processes. The recording therefore belongs to an account the candidate
cannot write to or signal, which is what makes it evidence.

## After

Evidence syncs to `s3://<bucket>/<seed>/evidence.tar.gz` every two minutes:
the recording, the fault timeline, and the score. On a host the candidate
owns the terminal, so there is no separate raw log and the recording is the
transcript. Pull the bundle and grade, pointing `--hints` at the ledger you
kept during the session:

```sh
aws s3 cp "$(terraform output -raw evidence_path)evidence.tar.gz" .
mkdir evidence && tar xzf evidence.tar.gz -C evidence
interviews grade sheet relay --seed calm-bison-0731 \
  --workdir evidence --hints ~/interviews/calm-bison-0731 -o sheet.md
```

Tear down with `terraform destroy`. If you forget, the host powers off at
the TTL (default 120 minutes) and terminates itself; the EIP and security
group still want the destroy.
