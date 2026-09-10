import importlib.util
import pathlib
import json
import subprocess
import sys
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("scrubber", pathlib.Path(__file__).with_name("scrub-artifact.py"))
scrubber = importlib.util.module_from_spec(spec)
spec.loader.exec_module(scrubber)

class ArtifactTests(unittest.TestCase):
    def test_codeql_fails_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            report = pathlib.Path(directory, "go.sarif")
            def check():
                return subprocess.run([sys.executable, str(pathlib.Path(__file__).with_name("check-codeql.py")), directory], capture_output=True, check=False).returncode
            self.assertNotEqual(check(), 0)
            for run in [{"results": [{"message": {"text": "finding"}}]},
                        {"invocations": [{"executionSuccessful": False}]},
                        {"invocations": [{"toolExecutionNotifications": [{"level": "error"}]}]}]:
                report.write_text(json.dumps({"runs": [run]}), encoding="utf-8")
                self.assertNotEqual(check(), 0)
            report.write_text(json.dumps({"runs": [{"results": [], "invocations": [{"executionSuccessful": True}]}]}), encoding="utf-8")
            self.assertEqual(check(), 0)

    def test_codeql_reports_safe_location_without_message(self):
        with tempfile.TemporaryDirectory() as directory:
            report = pathlib.Path(directory, "go.sarif")
            report.write_text(json.dumps({"runs": [{"results": [{
                "ruleId": "go/example",
                "message": {"text": "sensitive source detail"},
                "locations": [{"physicalLocation": {
                    "artifactLocation": {"uri": "internal/example.go"},
                    "region": {"startLine": 42},
                }}],
            }]}]}), encoding="utf-8")
            result = subprocess.run(
                [sys.executable, str(pathlib.Path(__file__).with_name("check-codeql.py")), directory],
                capture_output=True, text=True, check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("rule=go/example file=internal/example.go line=42", result.stderr)
            self.assertNotIn("sensitive source detail", result.stderr)

    def test_nested_paths(self):
        self.assertEqual(scrubber.scrub({"files": ["/home/runner/work/a", "C:\\Users\\Alice\\secret"]}), {"files": ["[REDACTED-PATH]", "[REDACTED-PATH]"]})

    def test_credentials_fail_closed(self):
        for value in ["Bearer " + "a" * 30, "ghp_" + "a" * 30, "-----BEGIN PRIVATE KEY-----"]:
            with self.assertRaises(ValueError):
                scrubber.scrub({"value": value})

    def test_inventory_preserved(self):
        data = {"name": "chromium", "version": "152.0.7977.82", "path": "/usr/bin/chromium"}
        self.assertEqual(scrubber.scrub(data), data)

    def test_generic_credentials_rejected(self):
        for data in [{"password": "hunter2"}, {"authorization": "Token " + "a" * 32},
                     {"access_token": "opaque"}, {"url": "https://alice:secret@example.org"},
                     {"value": "Token " + "a" * 32}]:
            with self.assertRaises(ValueError):
                scrubber.scrub(data)

    def test_unknown_inventory_rejected(self):
        with self.assertRaises(ValueError):
            scrubber.inventory({"environment": {"PRIVATE": "data"}})

    def test_syft_metadata_excluded(self):
        data = {"descriptor": {"name": "syft"}, "artifacts": [
            {"id": "pkg", "name": "chromium", "version": "152", "type": "deb",
             "metadata": {"arbitrary": "private-value"}, "licenses": [{"value": "MIT", "type": "declared"}]}],
            "source": {"target": "/home/runner/work"}, "configuration": {"arbitrary": "private-value"}}
        result = scrubber.inventory(data)
        self.assertNotIn("private-value", str(result))
        self.assertEqual(result["artifacts"][0]["name"], "chromium")

    def test_spdx_metadata_excluded(self):
        data = {"spdxVersion": "SPDX-2.3", "SPDXID": "SPDXRef-DOCUMENT", "dataLicense": "CC0-1.0",
                "name": "image", "documentNamespace": "https://example.org/sbom",
                "creationInfo": {"creators": ["Tool: syft"], "created": "2026-09-10T00:00:00Z"},
                "packages": [{"SPDXID": "SPDXRef-pkg", "name": "chromium", "versionInfo": "152",
                              "comment": "private-value"}], "annotations": [{"comment": "private-value"}]}
        result = scrubber.inventory(data)
        self.assertNotIn("private-value", str(result))
        self.assertEqual(result["packages"][0]["versionInfo"], "152")

if __name__ == "__main__":
    unittest.main()
