"""Fail closed on secrets and redact host paths before publishing JSON inventories.

Only JSON inventories are accepted; databases, archives and arbitrary logs are never
inputs. CI test output uses the stricter Go field-allowlist scrubber instead.
"""
import json
import pathlib
import re
import sys

SECRET = re.compile(r"(?:-----BEGIN [A-Z ]*PRIVATE KEY-----|(?:gh[pousr]_|github_pat_)[A-Za-z0-9_]{20,}|(?:AKIA|ASIA)[A-Z0-9]{16}|(?i:bearer\s+)[A-Za-z0-9._-]{12,})")
AUTH = re.compile(r"(?i)\b(?:bearer|token|basic)\s+[A-Za-z0-9+/._=-]{12,}|[a-z][a-z0-9+.-]*://[^/\s@]+@")
SENSITIVE_KEY = re.compile(r"(?i)^(?:authorization|proxy-authorization|password|passwd|secret|client[-_]?secret|api[-_]?key|access[-_]?token|refresh[-_]?token|id[-_]?token|token|cookie|set-cookie)$")
HOST = re.compile(r"(?:[A-Za-z]:[\\/][^\s\"<>]*|/(?:home|Users|tmp|root|workspace|private/var|var/folders|__w)/[^\s\"<>]*)")

def scrub(value):
    if isinstance(value, str):
        if SECRET.search(value) or AUTH.search(value):
            raise ValueError("credential pattern in artifact; upload refused")
        return HOST.sub("[REDACTED-PATH]", value)
    if isinstance(value, list):
        return [scrub(item) for item in value]
    if isinstance(value, dict):
        if any(SENSITIVE_KEY.fullmatch(key) for key in value):
            raise ValueError("credential field in artifact; upload refused")
        return {scrub(key): scrub(item) for key, item in value.items()}
    return value

def fields(value, allowed):
    """Copy only known string fields; never forward an unknown nested object."""
    if not isinstance(value, dict):
        raise ValueError("invalid inventory record")
    result = {}
    for key in allowed.split():
        if key in value:
            if not isinstance(value[key], str):
                raise ValueError("invalid inventory field type")
            result[key] = value[key]
    return result

def records(value, key, allowed):
    items = value.get(key, [])
    if not isinstance(items, list):
        raise ValueError("invalid inventory record list")
    return [fields(item, allowed) for item in items]

def inventory(value):
    """Recognize supported schemas and discard config, environment and metadata."""
    if not isinstance(value, dict):
        raise ValueError("inventory object required")
    # Reject recognizable credentials even in fields that would be discarded.
    value = scrub(value)
    if value.get("spdxVersion") in {"SPDX-2.2", "SPDX-2.3"}:
        result = fields(value, "spdxVersion SPDXID dataLicense name documentNamespace")
        if len(result) != 5:
            raise ValueError("incomplete SPDX inventory")
        creation = value.get("creationInfo", {})
        result["creationInfo"] = fields(creation, "created licenseListVersion")
        creators = creation.get("creators", [])
        if not isinstance(creators, list) or not creators or not all(isinstance(item, str) for item in creators):
            raise ValueError("SPDX creators required")
        result["creationInfo"]["creators"] = creators
        result["packages"] = []
        for package in value.get("packages", []):
            item = fields(package, "SPDXID name versionInfo supplier originator downloadLocation licenseConcluded licenseDeclared copyrightText sourceInfo")
            if "filesAnalyzed" in package:
                if not isinstance(package["filesAnalyzed"], bool):
                    raise ValueError("invalid filesAnalyzed")
                item["filesAnalyzed"] = package["filesAnalyzed"]
            item["checksums"] = records(package, "checksums", "algorithm checksumValue")
            item["externalRefs"] = records(package, "externalRefs", "referenceCategory referenceType referenceLocator")
            result["packages"].append(item)
        result["relationships"] = records(value, "relationships", "spdxElementId relationshipType relatedSpdxElement")
        result["files"] = []
        for file in value.get("files", []):
            item = fields(file, "SPDXID fileName licenseConcluded copyrightText")
            item["checksums"] = records(file, "checksums", "algorithm checksumValue")
            for key in ["fileTypes", "licenseInfoInFiles"]:
                if key in file:
                    if not isinstance(file[key], list) or not all(isinstance(part, str) for part in file[key]):
                        raise ValueError("invalid SPDX string list")
                    item[key] = file[key]
            result["files"].append(item)
    elif isinstance(value.get("descriptor"), dict) and value["descriptor"].get("name") == "syft":
        if not isinstance(value.get("artifacts"), list):
            raise ValueError("Syft artifacts required")
        result = {"schema": "invoice-license-inventory-v1", "artifacts": []}
        for package in value["artifacts"]:
            item = fields(package, "id name version type purl")
            item["licenses"] = records(package, "licenses", "value spdxExpression type")
            result["artifacts"].append(item)
    else:
        raise ValueError("unsupported inventory schema")
    return scrub(result)

if __name__ == "__main__":
    source, target = map(pathlib.Path, sys.argv[1:])
    if source.stat().st_size > 100 * 1024 * 1024:
        raise ValueError("artifact exceeds size limit")
    result = inventory(json.loads(source.read_text(encoding="utf-8")))
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(result, sort_keys=True) + "\n", encoding="utf-8")
