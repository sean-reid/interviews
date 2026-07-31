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
  -var problem=pipeline-meltdown \
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
run from boot. Log hints as you give them over SSM or SSH:
`interviews grade hint <problem> "text" --seed <seed> --minute <n>`.

## After

Evidence syncs to `s3://<bucket>/<seed>/evidence.tar.gz` every two minutes
and includes the recording, raw terminal log, fault timeline, score, and
hints. Pull it and grade:

```sh
aws s3 cp "$(terraform output -raw evidence_path)evidence.tar.gz" .
tar xzf evidence.tar.gz
interviews grade sheet <problem> --seed <seed> --workdir . -o sheet.md
```

Tear down with `terraform destroy`. If you forget, the host powers off at
the TTL (default 120 minutes) and terminates itself; the EIP and security
group still want the destroy.
