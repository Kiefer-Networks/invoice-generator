"""Fail closed on absent SARIF, analysis errors or CodeQL findings."""
import json
import pathlib
import sys

reports = list(pathlib.Path(sys.argv[1]).glob("*.sarif"))
if not reports:
    raise SystemExit("CodeQL produced no SARIF")
for path in reports:
    data = json.loads(path.read_text(encoding="utf-8"))
    if not data.get("runs"):
        raise SystemExit("CodeQL produced no analysis runs")
    for run in data["runs"]:
        results = run.get("results", [])
        for result in results:
            location = (result.get("locations") or [{}])[0].get("physicalLocation", {})
            artifact = location.get("artifactLocation", {}).get("uri", "unknown")
            line = location.get("region", {}).get("startLine", "unknown")
            print(f"CodeQL finding: rule={result.get('ruleId', 'unknown')} file={artifact} line={line}", file=sys.stderr)
        if results:
            raise SystemExit("CodeQL findings require review; raw report stays private")
        for invocation in run.get("invocations", []):
            if invocation.get("executionSuccessful") is False:
                raise SystemExit("CodeQL analysis unsuccessful")
            if any(item.get("level") == "error" for item in invocation.get("toolExecutionNotifications", [])):
                raise SystemExit("CodeQL analysis reported an error")
print("CodeQL analysis complete: no findings")
