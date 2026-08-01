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

Put the key in a named profile so it is never your default identity, and pick a
name nothing else uses:

```sh
aws configure --profile interviews-personal   # paste the key and the region
export AWS_PROFILE=interviews-personal
aws sts get-caller-identity                   # check the account is the one you meant
```

Check that last line rather than assume it. A profile name that collides with an
SSO profile you already have shadows it silently, and the failure mode is
terraform building an interview host in someone else's account. If your
organisation manages `~/.aws/config` with a tool, keep `region` in the
credentials file entry instead, or a regeneration will drop it.

The policy pins a region, so provisioning somewhere else fails with
`UnauthorizedOperation` until you update it. That is deliberate: a typo in
`-var region=` cannot quietly build a host on the other side of the world.

After editing the policy, re-attach it. The file in this repository and the
policy on the user are separate things, and a stale attachment is invisible
until something is denied:

```sh
aws iam put-user-policy --user-name interviews-terraform \
  --policy-name interviews-terraform --policy-document file:///tmp/iv-terraform-policy.json
```

The policy has been run end to end against a real account: it creates the
bucket, provisions a host with its role and instance profile, and destroys all
of it. Four actions were missing when it was written from reading the modules,
which is worth knowing if you extend them. A data source reads attributes as
well as resources, and the console actions are there for operability rather
than for terraform: a host has no ssh, so `ec2:GetConsoleOutput` is the only
way to see one fail from outside.

You can check an attachment without provisioning anything. A call that comes
back `NoSuchEntity` was permitted; one that comes back `AccessDenied` was not:

```sh
aws iam get-role --role-name iv-probe-does-not-exist   # NoSuchEntity: allowed
aws iam get-role --role-name SomeOtherRole             # AccessDenied: correctly scoped
aws ec2 describe-vpcs --region <another-region>        # AccessDenied: region lock works
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

Two details worth knowing before editing the file. `iam:PassRole` has no matching
API call: it is a permission the console and terraform check, and deleting it
because it does not appear in the API reference breaks the instance profile
attachment. And every other action name is an API operation, which is checkable
offline against the model the AWS CLI ships:

```sh
python3 - <<'EOF'
import json
base = "/usr/local/aws-cli/awscli/botocore/data"
ops = set(json.load(open(f"{base}/ec2/2016-11-15/service-2.json"))["operations"])
print("DescribeAddressesAttribute" in ops)
EOF
```

That check is how `ec2:DescribeInstanceMetadataDefaults` came out of this policy:
it does not exist. The real names are `GetInstanceMetadataDefaults` and
`ModifyInstanceMetadataDefaults`, and neither is needed, because
`metadata_options` goes out with `RunInstances` and reads back through
`DescribeInstances`. Changing it on a host that is already up would need
`ec2:ModifyInstanceMetadataOptions`, which no documented flow here does: each
interview gets a fresh host.

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
