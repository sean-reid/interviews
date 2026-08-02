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
"""

import datetime
import os

OWNER_TAG = "ManagedBy"
OWNER = "interviews"
SEED_TAG = "Interview"
TTL_TAG = "TTLMinutes"

# The in-guest timer should always win. Waiting past the TTL before acting
# means a host shutting itself down on schedule is never raced, and what this
# terminates is only ever a host whose own timer did not fire.
DEFAULT_GRACE_MINUTES = 15


def grace_minutes():
    return int(os.environ.get("REAPER_GRACE_MINUTES", DEFAULT_GRACE_MINUTES))


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

    if not doomed:
        return {"terminated": []}

    # Terminating is the whole point, so a failure here has to be loud: the
    # alternative is a host that bills on while a run reports success.
    ec2.terminate_instances(InstanceIds=doomed)
    print(f"terminated {len(doomed)}: {', '.join(doomed)}")
    return {"terminated": doomed}
