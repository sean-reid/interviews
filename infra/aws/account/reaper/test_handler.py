"""Tests for the reaper's decision, which is the part that deletes things."""

import datetime
import unittest

import handler

NOW = datetime.datetime(2026, 8, 2, 12, 0, tzinfo=datetime.timezone.utc)
GRACE = 15


def instance(minutes_ago, **tags):
    full = {handler.OWNER_TAG: handler.OWNER, handler.SEED_TAG: "calm-bison-0801"}
    full.update(tags)
    return {
        "InstanceId": "i-0123456789abcdef0",
        "LaunchTime": NOW - datetime.timedelta(minutes=minutes_ago),
        "Tags": [{"Key": k, "Value": v} for k, v in full.items() if v is not None],
    }


class Reapable(unittest.TestCase):
    def test_terminates_a_host_past_its_ttl_and_grace(self):
        iid, why = handler.reapable(instance(140, TTLMinutes="120"), NOW, GRACE)
        self.assertIsNotNone(iid, why)

    def test_spares_a_host_inside_its_ttl(self):
        iid, why = handler.reapable(instance(30, TTLMinutes="120"), NOW, GRACE)
        self.assertIsNone(iid, why)

    def test_spares_a_host_inside_the_grace(self):
        # Past the ttl but not past the grace: the guest's own timer should
        # win, and racing it is how this ends up killing a healthy interview.
        iid, why = handler.reapable(instance(125, TTLMinutes="120"), NOW, GRACE)
        self.assertIsNone(iid, why)

    def test_spares_anything_it_cannot_name(self):
        for name, inst in [
            ("no owner tag", instance(999, **{handler.OWNER_TAG: "something-else", "TTLMinutes": "1"})),
            ("no seed tag", instance(999, **{handler.SEED_TAG: None, "TTLMinutes": "1"})),
            ("no ttl tag", instance(999)),
            ("unparseable ttl", instance(999, TTLMinutes="soon")),
            ("zero ttl", instance(999, TTLMinutes="0")),
        ]:
            with self.subTest(name):
                iid, why = handler.reapable(inst, NOW, GRACE)
                self.assertIsNone(iid, f"{name}: terminated anyway ({why})")

    def test_the_reason_is_always_reported(self):
        # Every decision is logged, so a spared host is never silent.
        for inst in [instance(140, TTLMinutes="120"), instance(5, TTLMinutes="120"), instance(5)]:
            _, why = handler.reapable(inst, NOW, GRACE)
            self.assertTrue(why, "a decision was made with no reason given")


AGE = 24
IV_TAGS = [
    {"Key": handler.OWNER_TAG, "Value": handler.OWNER},
    {"Key": handler.SEED_TAG, "Value": "calm-bison-0801"},
]


def hours_ago(hours):
    return NOW - datetime.timedelta(hours=hours)


class Orphaned(unittest.TestCase):
    def test_deletes_an_old_tagged_unused_leftover(self):
        tags = {handler.OWNER_TAG: handler.OWNER, handler.SEED_TAG: "calm-bison-0801"}
        doomed, why = handler.orphaned("role", "iv-calm-bison-0801-abc", tags,
                                       hours_ago(25), False, NOW, AGE)
        self.assertTrue(doomed, why)

    def test_spares_everything_it_cannot_prove_orphaned(self):
        tags = {handler.OWNER_TAG: handler.OWNER, handler.SEED_TAG: "calm-bison-0801"}
        for name, args in [
            ("outside the prefix", ("role", "other-role", tags, hours_ago(999), False)),
            # The reaper's own role is iv-reaper, created by the account
            # module with no tags: the tag guard is its self-preservation.
            ("untagged, like iv-reaper", ("role", "iv-reaper", {}, hours_ago(999), False)),
            ("no seed tag", ("role", "iv-x", {handler.OWNER_TAG: handler.OWNER}, hours_ago(999), False)),
            ("still in use", ("instance profile", "iv-x", tags, hours_ago(999), True)),
            ("young enough to be an apply", ("role", "iv-x", tags, hours_ago(2), False)),
        ]:
            with self.subTest(name):
                doomed, why = handler.orphaned(*args, NOW, AGE)
                self.assertFalse(doomed, f"{name}: deleted anyway ({why})")
                self.assertTrue(why, "a decision was made with no reason given")


class FakePaginator:
    def __init__(self, pages):
        self._pages = pages

    def paginate(self, **kwargs):
        return self._pages()


class FakeEC2:
    """Reports one instance carrying each given profile arn, plus one with
    no profile at all, which real accounts have plenty of."""

    def __init__(self, profile_arns):
        self._arns = profile_arns

    def get_paginator(self, name):
        assert name == "describe_instances"
        instances = [{"IamInstanceProfile": {"Arn": arn}} for arn in self._arns] + [{}]
        return FakePaginator(lambda: [{"Reservations": [{"Instances": instances}]}])


class FakeIAM:
    def __init__(self, profiles, roles):
        self.profiles = {p["InstanceProfileName"]: p for p in profiles}
        self.roles = {r["RoleName"]: r for r in roles}
        self.calls = []

    def get_paginator(self, name):
        if name == "list_instance_profiles":
            return FakePaginator(lambda: [{"InstanceProfiles": list(self.profiles.values())}])
        assert name == "list_roles"
        return FakePaginator(lambda: [{"Roles": list(self.roles.values())}])

    def list_instance_profile_tags(self, InstanceProfileName):
        return {"Tags": self.profiles[InstanceProfileName].get("Tags", [])}

    def list_role_tags(self, RoleName):
        return {"Tags": self.roles[RoleName].get("Tags", [])}

    def list_instance_profiles_for_role(self, RoleName):
        return {"InstanceProfiles": [p for p in self.profiles.values()
                                     if any(r["RoleName"] == RoleName for r in p["Roles"])]}

    def list_role_policies(self, RoleName):
        return {"PolicyNames": self.roles[RoleName].get("Policies", [])}

    def delete_role_policy(self, RoleName, PolicyName):
        self.calls.append(f"delete_role_policy {RoleName} {PolicyName}")

    def remove_role_from_instance_profile(self, InstanceProfileName, RoleName):
        self.calls.append(f"remove_role {InstanceProfileName} {RoleName}")
        p = self.profiles[InstanceProfileName]
        p["Roles"] = [r for r in p["Roles"] if r["RoleName"] != RoleName]

    def delete_instance_profile(self, InstanceProfileName):
        self.calls.append(f"delete_profile {InstanceProfileName}")
        del self.profiles[InstanceProfileName]

    def delete_role(self, RoleName):
        self.calls.append(f"delete_role {RoleName}")
        del self.roles[RoleName]


def profile(name, arn, hours_old, roles, tags=None):
    return {"InstanceProfileName": name, "Arn": arn, "CreateDate": hours_ago(hours_old),
            "Roles": [{"RoleName": r} for r in roles], "Tags": tags or []}


def role(name, hours_old, tags=None, policies=None):
    return {"RoleName": name, "CreateDate": hours_ago(hours_old),
            "Tags": tags or [], "Policies": policies or []}


class SweepIAM(unittest.TestCase):
    def test_sweeps_an_orphaned_pair_and_nothing_else(self):
        iam = FakeIAM(
            profiles=[
                profile("iv-live-abc", "arn:live", 30, ["iv-live-role"], IV_TAGS),
                profile("iv-orphan-abc", "arn:orphan", 30, ["iv-orphan-role"], IV_TAGS),
            ],
            roles=[
                role("iv-live-role", 30, IV_TAGS, ["evidence-and-tarball"]),
                role("iv-orphan-role", 30, IV_TAGS, ["evidence-and-tarball"]),
                role("iv-reaper", 999),
                role("something-else", 999),
            ],
        )
        deleted = handler.sweep_iam(iam, FakeEC2(["arn:live"]), NOW, AGE)
        self.assertEqual(deleted, ["iv-orphan-abc", "iv-orphan-role"])
        self.assertIn("iv-live-abc", iam.profiles)
        self.assertIn("iv-live-role", iam.roles)
        self.assertIn("iv-reaper", iam.roles)
        self.assertIn("something-else", iam.roles)
        # The order the API demands: detach, drop the profile, strip the
        # inline policy, then the role.
        self.assertEqual(iam.calls, [
            "remove_role iv-orphan-abc iv-orphan-role",
            "delete_profile iv-orphan-abc",
            "delete_role_policy iv-orphan-role evidence-and-tarball",
            "delete_role iv-orphan-role",
        ])

    def test_a_young_orphan_waits(self):
        iam = FakeIAM(
            profiles=[profile("iv-young-abc", "arn:young", 2, ["iv-young-role"], IV_TAGS)],
            roles=[role("iv-young-role", 2, IV_TAGS)],
        )
        deleted = handler.sweep_iam(iam, FakeEC2([]), NOW, AGE)
        self.assertEqual(deleted, [])
        self.assertIn("iv-young-abc", iam.profiles)
        self.assertIn("iv-young-role", iam.roles)

    def test_a_role_attached_to_a_surviving_profile_is_spared(self):
        # The profile is untagged so the sweep cannot take it, and the role,
        # however orphaned it looks, must not be yanked out from under it.
        iam = FakeIAM(
            profiles=[profile("iv-untagged-abc", "arn:u", 999, ["iv-held-role"])],
            roles=[role("iv-held-role", 999, IV_TAGS)],
        )
        deleted = handler.sweep_iam(iam, FakeEC2([]), NOW, AGE)
        self.assertEqual(deleted, [])
        self.assertIn("iv-held-role", iam.roles)


class Expired(unittest.TestCase):
    def test_boundary_is_inclusive_of_the_deadline(self):
        launched = NOW - datetime.timedelta(minutes=135)
        self.assertTrue(handler.expired(NOW, launched, 120, GRACE))
        self.assertFalse(handler.expired(NOW, launched, 120, GRACE + 1))


if __name__ == "__main__":
    unittest.main()
