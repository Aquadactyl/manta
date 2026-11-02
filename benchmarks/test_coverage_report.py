import unittest
from decimal import Decimal

from coverage_report import summarize


class CoverageReportTest(unittest.TestCase):
    def test_duplicate_blocks_merge_hits_without_changing_denominator(self):
        report = summarize("mode: atomic\nexample/a.go:1.1,2.1 3 0\nexample/a.go:1.1,2.1 3 2\n")
        self.assertEqual((report["covered"], report["statements"]), (3, 3))
        self.assertTrue(report["targetMet"])

    def test_uncovered_blocks_remain_visible(self):
        report = summarize("mode: atomic\nexample/a.go:1.1,2.1 3 0\n")
        self.assertEqual(report["uncovered"], 3)
        self.assertEqual(report["files"][0]["uncoveredBlocks"], [{"span": "1.1,2.1", "statements": 3}])
        self.assertFalse(report["targetMet"])

    def test_display_rounding_cannot_pass_the_exact_target(self):
        report = summarize("mode: atomic\nexample/a.go:1.1,2.1 1000000000 1\nexample/a.go:3.1,4.1 1 0\n")
        self.assertEqual(report["percent"], 100.0)
        self.assertFalse(report["targetMet"])

    def test_threshold_uses_statement_counts(self):
        profile = "mode: atomic\nexample/a.go:1.1,2.1 3 1\nexample/a.go:3.1,4.1 1 0\n"
        self.assertTrue(summarize(profile, Decimal(75))["targetMet"])
        self.assertFalse(summarize(profile, Decimal("75.01"))["targetMet"])

    def test_invalid_profiles_are_rejected(self):
        for profile in ["", "mode: atomic\n", "mode: set\n", "mode: atomic\na:1 1 -1\n", "mode: atomic\na:1 2 1\na:1 3 1\n"]:
            with self.subTest(profile=profile), self.assertRaises(ValueError):
                summarize(profile)


if __name__ == "__main__":
    unittest.main()
