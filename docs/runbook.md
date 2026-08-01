# Live interview runbook

One disposable EC2 host per interview: broken environment, shared recorded
terminal, TTL self-destruct. Evidence outlives the host in S3.

## The AWS identity terraform runs as

Make a dedicated IAM user for this rather than using your own. Everything the
modules create is named `iv-<seed>-`, which is what lets the policy stay scoped:
the user can create roles and instance profiles under that prefix and nothing
else, and it can only touch the one evidence bucket.

[`infra/aws/terraform-policy.json`](../infra/aws/terraform-policy.json) is the
policy. Replace the three placeholders first:

- `ACCOUNT_ID` with your twelve digit account id
- `REGION` with the region you provision in, for example `eu-west-1`
- `BUCKET` with the evidence bucket name you are about to create

Then:

```sh
account=$(aws sts get-caller-identity --query Account --output text)
sed -e "s/ACCOUNT_ID/$account/g" -e "s/REGION/eu-west-1/g" -e "s/BUCKET/my-interview-evidence/g"   infra/aws/terraform-policy.json >/tmp/iv-terraform-policy.json

aws iam create-user --user-name interviews-terraform
aws iam put-user-policy --user-name interviews-terraform   --policy-name interviews-terraform --policy-document file:///tmp/iv-terraform-policy.json
aws iam create-access-key --user-name interviews-terraform
```

Put the key in a named profile and point terraform at it with
`AWS_PROFILE=interviews`, so it is never your default identity:

```sh
aws configure --profile interviews        # paste the key and the region
export AWS_PROFILE=interviews
aws sts get-caller-identity              # says interviews-terraform
```

What the policy allows, and why each part is there:

| Statement | Why |
|---|---|
| `sts:GetCallerIdentity` | the provider calls it on every plan |
| network and image reads | the modules look up the default VPC, its subnets, and the Ubuntu AMI |
| session host lifecycle | security group, elastic IP, instance, and their tags |
| role and profile under `iv-*` | the host needs an instance role, and only under that prefix |
| `iam:PassRole` to ec2 only | attaching that role to the instance, and nothing else |
| the evidence bucket | create it once, then write evidence and read the tarball |

`Describe*` calls cannot be scoped to a resource, so those are `"Resource": "*"`
with a region condition. Everything that can be scoped is.

The instance gets its own much smaller role, written by the module: it may put
objects under `s3://<bucket>/<seed>/` and get the content tarball, nothing more.
A candidate on the host inherits that and no more.

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
  -var problem=<problem> \
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
interviews grade hint <problem> "asked what the health endpoint returns" \
  --seed calm-bison-0731 --minute 9 --workdir ~/interviews/calm-bison-0731
```

## The app route

On a problem that declares an app, `terraform output -raw app_url` is a third
token route, shared by candidate and observer because it serves the same broken
app to both. Expect it to fail for much of the session: that is the app being
broken, not the host. Caddy's `/healthz` does not touch it, so the TTL watchdog
keeps working while the app is down.

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
interviews grade sheet <problem> --seed calm-bison-0731 \
  --workdir evidence --hints ~/interviews/calm-bison-0731 -o sheet.md
```

Tear down with `terraform destroy`. If you forget, the host powers off at
the TTL (default 120 minutes) and terminates itself; the EIP and security
group still want the destroy.
