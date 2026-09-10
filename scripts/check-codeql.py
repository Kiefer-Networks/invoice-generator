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
        if run.get("results"):
            raise SystemExit("CodeQL findings require review; raw report stays private")
        for invocation in run.get("invocations", []):
            if invocation.get("executionSuccessful") is False:
                raise SystemExit("CodeQL analysis unsuccessful")
            if any(item.get("level") == "error" for item in invocation.get("toolExecutionNotifications", [])):
                raise SystemExit("CodeQL analysis reported an error")
print("CodeQL analysis complete: no findings")
