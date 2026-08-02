# Live interview runbook

One disposable EC2 host per interview: broken environment, shared recorded
terminal, TTL self-destruct. Evidence outlives the host in S3.

## Testing a host without provisioning one

`session/host/offline-test.sh` runs the whole of provision.sh in a container,
offline, in about two minutes, and then checks what it left behind: the two
accounts and that the candidate cannot read the content, the unpacked platform,
the sudoers syntax, the rendered Caddyfile with its tokens and no placeholders,
and the order the units would start in. CI runs it on every change.

Use it before provisioning anything. Both bugs that reached a real host, a
package that does not exist on this release and a docker without its compose
plugin, would have failed there first, and a real provision takes a quarter of
an hour to tell you.

What it cannot cover is the units actually running, since a container has no
systemd: the environment build, the fault injection, and the session stack with
its two accounts. For those, provision a host and read
`interviews sessions log --follow`.

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
| the evidence bucket | create and configure it once, then write evidence and read the tarball; the long list of bucket reads is terraform reading back `aws_s3_bucket` |

`Describe*` calls cannot be scoped to a resource, so those are `"Resource": "*"`
with a region condition. Everything that can be scoped is.

Two details worth knowing before editing the file. `iam:PassRole` has no matching
API call: it is a permission the console and terraform check, and deleting it
because it does not appear in the API reference breaks the instance profile
attachment. And several of the S3 bucket actions abbreviate their API operation:
`s3:GetLifecycleConfiguration` is what authorizes `GetBucketLifecycleConfiguration`,
and `s3:GetBucketPublicAccessBlock` authorizes `GetPublicAccessBlock`, so check
those against the S3 page of the service authorization reference. Every other
action name is an API operation, which is checkable offline against the model
the AWS CLI ships:

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

`interviews setup aws` creates the evidence bucket, then builds and uploads
the bundle a host downloads at boot: the linux binary, the content tree, and
the host scripts.

```sh
interviews setup aws --region eu-west-1 --bucket my-interview-evidence
```

Re-run it after a platform release or a content change; `--skip-bucket`
leaves the bucket alone and only refreshes the bundle.

## Provision a session

```sh
interviews start <problem> --remote
```

It provisions the host, watches boot until the environment is built, and
prints the candidate, observer, and app URLs; `interviews sessions show`
reprints them later, and `interviews sessions log --follow` shows what the
host said as it booted. `--ttl` moves the self-destruct from its 120 minute
default.

## During

Open the observer URL yourself: read-only, invisible to the candidate. Send
the candidate URL at start time, not before; the recording and the TTL clock
run from boot.

Log hints as you give them, from your own machine. The host has no key pair,
no port 22, and no SSM, so there is no shell on it to log from. On the
machine that ran `start`:

```sh
interviews hint "asked what the health endpoint returns" --minute 9
```

From any other machine, `interviews grade hint <problem> "..." --minute 9
--seed calm-bison-0731` logs against the seed instead.

## The app route

On a problem that declares an app, `start` prints a third token route,
shared by candidate and observer because it serves the same broken app to
both. Expect it to fail for much of the session: that is the app being
broken, not the host. Caddy's `/healthz` does not touch it, so the health
endpoint stays green while the app is down.

## The two accounts

The candidate URL is a shell on the `candidate` account, which owns the tmux
server the browser attaches to. It cannot read `/opt/interviews`, so the
answer keys are out of reach, and on kubernetes problems its kubectl is a
service account scoped to the scenario namespace. Provisioning fails rather
than continuing if the candidate can reach the content tree.

The `interviewer` account runs the platform, the recorder, and both ttyd
processes. The recording therefore belongs to an account the candidate
cannot write to or signal. The tmux server is theirs, though, so they can
end the client the recorder watches. A recorder that exits while the
session is still up restarts into a numbered cast segment
(`session.cast.1`, ...) and the gap is logged with a timestamp in
`recorder.log`; both ship in the evidence bundle, so ending the recorder
costs seconds of recording and documents itself.

## After

Evidence syncs to `s3://<bucket>/<seed>/evidence.tar.gz` every two minutes:
the recording, the fault timeline, and the score. Its `state.json` also says
what the environment was produced on, read as it came up: the node image, the
kind and kubectl versions, the platform version, the content the box
unpacked, and whether it ran here or on a host. Two candidates on one seeded
problem only compare when those match. On a host the candidate owns the
terminal, so there is no separate raw log and the recording is the
transcript. End the session from the machine that started it:

```sh
interviews end calm-bison-0731
```

It pulls the evidence down, destroys the host, and prints where the local
copy landed; the bucket keeps the synced original. Then grade, pointing
`--workdir` at the pulled evidence; hints logged with `interviews hint` are
merged in by seed on their own:

```sh
interviews grade sheet <problem> --seed calm-bison-0731 --workdir <evidence dir> -o sheet.md
```

If you forget to end, the host powers off at the TTL (default 120 minutes)
and terminates itself; `end` still wants running for the EIP and security
group.

## The backstop

`interviews end` and the guest's own TTL timer are the two ways a host stops.
Both can fail: a kernel panic, a full disk, or a provision that died before
arming anything. So the account runs a reaper, created by the account module:
every 15 minutes it terminates instances whose `TTLMinutes` tag is more than
15 minutes past their launch time.

It only touches instances carrying `ManagedBy=interviews`, `Interview` and
`TTLMinutes`, and its IAM can terminate nothing else. It also deletes the
`iv-` roles and instance profiles the interview module tagged once they are a
day old with no instance attached: a destroy that dies partway strands them,
they cost nothing so no bill surfaces them, and instance profiles cap at
1000 per account. Everything it looked at and why is in
`/aws/lambda/iv-reaper`:

```sh
aws logs tail /aws/lambda/iv-reaper --since 1h
```

The grace exists so the guest's own timer always wins. If you need a host to
outlive its TTL, remove its `TTLMinutes` tag rather than racing the schedule.

## Finding a host nobody is watching

The session registry lives on the machine that ran `start`, so it cannot say
what is running anywhere else. `interviews sessions --remote` asks the account
instead, matching the tags the module puts on every resource:

```sh
interviews sessions --remote
```

It shows three things the local list cannot: a host another machine
provisioned, marked `*`; a host still up after `end` reported it destroyed;
and elastic IPs attached to nothing. Run it after any interrupted provision. A
terminated instance stops costing on its own, an elastic IP does not.
