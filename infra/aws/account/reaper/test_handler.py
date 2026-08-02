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


class Expired(unittest.TestCase):
    def test_boundary_is_inclusive_of_the_deadline(self):
        launched = NOW - datetime.timedelta(minutes=135)
        self.assertTrue(handler.expired(NOW, launched, 120, GRACE))
        self.assertFalse(handler.expired(NOW, launched, 120, GRACE + 1))


if __name__ == "__main__":
    unittest.main()
