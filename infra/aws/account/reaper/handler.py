"""Terminate interview hosts that outlived their TTL.

Three things have to work for a host to stop billing: a systemd timer inside
the guest, someone remembering `interviews end`, and someone running
`interviews sessions --remote`. The first two live in the same failure domain
as the host itself, and the third lives in a person's head. This is the layer
outside the guest: it reads the tags the interview module writes and does not
depend on anything running on the box.

It only ever terminates an instance carrying all three of ManagedBy,
Interview and TTLMinutes. Anything missing one is logged and left alone,
because an instance this cannot name is not one it should delete.

It also sweeps the IAM role and instance profile the interview module makes
per session. A destroy that dies partway strands them, they cost nothing so
no bill surfaces them, the terraform identity is deliberately denied
ListRoles so no listing shows them, and instance profiles cap at 1000 per
account. Only resources under the iv- prefix, tagged by the module, older
than a day, and attached to no instance are touched.
"""

import datetime
import os

OWNER_TAG = "ManagedBy"
OWNER = "interviews"
SEED_TAG = "Interview"
TTL_TAG = "TTLMinutes"

# Everything the interview module names starts with this.
ROLE_PREFIX = "iv-"

# The in-guest timer should always win. Waiting past the TTL before acting
# means a host shutting itself down on schedule is never raced, and what this
# terminates is only ever a host whose own timer did not fire.
DEFAULT_GRACE_MINUTES = 15

# How old a role or instance profile with no instance must be before it
# counts as orphaned rather than as an apply still in flight. A day is far
# past any provision; ttl does not bound it because a role outliving its
# host is exactly the leak being swept.
DEFAULT_ORPHAN_AGE_HOURS = 24


def grace_minutes():
    return int(os.environ.get("REAPER_GRACE_MINUTES", DEFAULT_GRACE_MINUTES))


def orphan_age_hours():
    return int(os.environ.get("REAPER_ORPHAN_AGE_HOURS", DEFAULT_ORPHAN_AGE_HOURS))


def tags_of(instance):
    return {t["Key"]: t["Value"] for t in instance.get("Tags", [])}


def expired(now, launched_at, ttl_minutes, grace):
    """Whether a host launched at launched_at is past its TTL plus the grace."""
    deadline = launched_at + datetime.timedelta(minutes=ttl_minutes + grace)
    return now >= deadline


def reapable(instance, now, grace):
    """The instance id to terminate, or None with the reason it was spared."""
    tags = tags_of(instance)
    iid = instance.get("InstanceId", "unknown")
    if tags.get(OWNER_TAG) != OWNER:
        return None, f"{iid}: not tagged {OWNER_TAG}={OWNER}"
    if not tags.get(SEED_TAG):
        return None, f"{iid}: no {SEED_TAG} tag, so nothing can name it"
    raw = tags.get(TTL_TAG)
    if not raw:
        return None, f"{iid}: no {TTL_TAG} tag, so it has no deadline"
    try:
        ttl = int(raw)
    except ValueError:
        return None, f"{iid}: {TTL_TAG}={raw!r} is not a number"
    if ttl <= 0:
        return None, f"{iid}: {TTL_TAG}={ttl} means no deadline"
    if not expired(now, instance["LaunchTime"], ttl, grace):
        return None, f"{iid}: {tags[SEED_TAG]} is still inside its {ttl}m ttl"
    return iid, f"{iid}: {tags[SEED_TAG]} is past its {ttl}m ttl plus {grace}m grace"


def orphaned(kind, name, tags, created_at, in_use, now, age_hours):
    """Whether an iv- role or instance profile should go, with the reason.

    Deleting IAM is scarier than terminating a tagged instance, so every
    guard fails safe: the module's prefix, the module's tags (which the
    reaper's own untagged iv-reaper role does not carry), nothing still
    using it, and an age no provision in flight could reach.
    """
    what = f"{kind} {name}"
    if not name.startswith(ROLE_PREFIX):
        return False, f"{what}: not under the {ROLE_PREFIX} prefix"
    if tags.get(OWNER_TAG) != OWNER:
        return False, f"{what}: not tagged {OWNER_TAG}={OWNER}"
    seed = tags.get(SEED_TAG)
    if not seed:
        return False, f"{what}: no {SEED_TAG} tag, so nothing can name it"
    if in_use:
        return False, f"{what}: still in use"
    if now < created_at + datetime.timedelta(hours=age_hours):
        return False, f"{what}: younger than {age_hours}h, so its apply may still be running"
    return True, f"{what}: {seed} left it behind"


def profiles_in_use(ec2):
    """Instance profile ARNs some non-terminated instance still carries.

    Every instance, not just tagged ones: whatever is attached to anything
    that exists is not an orphan, whoever owns the instance.
    """
    arns = set()
    pages = ec2.get_paginator("describe_instances").paginate(
        Filters=[{"Name": "instance-state-name",
                  "Values": ["pending", "running", "shutting-down", "stopping", "stopped"]}]
    )
    for page in pages:
        for reservation in page["Reservations"]:
            for instance in reservation["Instances"]:
                arn = instance.get("IamInstanceProfile", {}).get("Arn")
                if arn:
                    arns.add(arn)
    return arns


def iam_tags(page_of_tags):
    return {t["Key"]: t["Value"] for t in page_of_tags}


def sweep_iam(iam, ec2, now, age_hours):
    """Delete the iv- roles and instance profiles no instance carries.

    Profiles first: removing a doomed profile's roles is what frees those
    roles for the pass below, so one run cleans a whole orphaned pair. A
    role still attached to any surviving profile is left alone.
    """
    in_use = profiles_in_use(ec2)
    deleted = []
    for page in iam.get_paginator("list_instance_profiles").paginate():
        for profile in page["InstanceProfiles"]:
            name = profile["InstanceProfileName"]
            tags = {}
            if name.startswith(ROLE_PREFIX):
                tags = iam_tags(iam.list_instance_profile_tags(InstanceProfileName=name)["Tags"])
            doomed, why = orphaned("instance profile", name, tags, profile["CreateDate"],
                                   profile["Arn"] in in_use, now, age_hours)
            print(why)
            if not doomed:
                continue
            for role in profile["Roles"]:
                iam.remove_role_from_instance_profile(InstanceProfileName=name,
                                                      RoleName=role["RoleName"])
            iam.delete_instance_profile(InstanceProfileName=name)
            deleted.append(name)
    for page in iam.get_paginator("list_roles").paginate():
        for role in page["Roles"]:
            name = role["RoleName"]
            tags = {}
            attached = []
            if name.startswith(ROLE_PREFIX):
                # ListRoles reports no tags, so they cost a call per iv- role.
                tags = iam_tags(iam.list_role_tags(RoleName=name)["Tags"])
                attached = iam.list_instance_profiles_for_role(RoleName=name)["InstanceProfiles"]
            doomed, why = orphaned("role", name, tags, role["CreateDate"],
                                   bool(attached), now, age_hours)
            print(why)
            if not doomed:
                continue
            for policy in iam.list_role_policies(RoleName=name)["PolicyNames"]:
                iam.delete_role_policy(RoleName=name, PolicyName=policy)
            iam.delete_role(RoleName=name)
            deleted.append(name)
    return deleted


def handler(event, context):
    # Imported here, not at module scope: boto3 exists in the Lambda runtime
    # and nowhere else, and the decision above is what the tests exercise.
    import boto3

    ec2 = boto3.client("ec2")
    now = datetime.datetime.now(datetime.timezone.utc)
    grace = grace_minutes()

    doomed = []
    pages = ec2.get_paginator("describe_instances").paginate(
        Filters=[
            {"Name": f"tag:{OWNER_TAG}", "Values": [OWNER]},
            {"Name": "instance-state-name", "Values": ["pending", "running", "stopping", "stopped"]},
        ]
    )
    for page in pages:
        for reservation in page["Reservations"]:
            for instance in reservation["Instances"]:
                iid, why = reapable(instance, now, grace)
                print(why)
                if iid:
                    doomed.append(iid)

    if doomed:
        # Terminating is the whole point, so a failure here has to be loud:
        # the alternative is a host that bills on while a run reports success.
        ec2.terminate_instances(InstanceIds=doomed)
        print(f"terminated {len(doomed)}: {', '.join(doomed)}")

    iam_deleted = sweep_iam(boto3.client("iam"), ec2, now, orphan_age_hours())
    if iam_deleted:
        print(f"deleted {len(iam_deleted)} orphaned iam resources: {', '.join(iam_deleted)}")
    return {"terminated": doomed, "iam_deleted": iam_deleted}
