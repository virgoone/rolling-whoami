#!/usr/bin/env python3
"""Sample a rolling deployment. No retries: every failed request is counted."""
import argparse
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
import json
from pathlib import Path
import threading
import time
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("url")
    parser.add_argument("--seconds", type=int, default=120)
    parser.add_argument("--workers", type=int, default=4)
    parser.add_argument("--interval", type=float, default=0.2)
    parser.add_argument("--output", default="probe.jsonl")
    args = parser.parse_args()
    if args.seconds < 1 or not 1 <= args.workers <= 16 or args.interval < 0.05:
        parser.error("seconds >= 1, workers 1..16, interval >= 0.05 required")
    stop_at = time.monotonic() + args.seconds
    lock = threading.Lock()
    rows = []

    def worker(index, output):
        slow = index == args.workers  # One extra worker tests requests spanning route changes.
        path = "/wait?duration=5s" if slow else "/"
        while time.monotonic() < stop_at:
            started = time.monotonic()
            row = {"at": datetime.now(timezone.utc).isoformat(), "kind": "slow" if slow else "normal"}
            try:
                request = urllib.request.Request(args.url.rstrip("/") + path, headers={"Cache-Control": "no-cache"})
                with urllib.request.urlopen(request, timeout=12) as response:
                    data = json.load(response)
                    row.update(status=response.status, version=data["version"], instance=data["instance_id"])
                    if response.status != 200:
                        row["error"] = "unexpected HTTP status"
            except Exception as error:
                row["error"] = str(error)
            row["latency_ms"] = round((time.monotonic() - started) * 1000, 2)
            with lock:
                rows.append(row)
                output.write(json.dumps(row) + "\n")
                output.flush()
            time.sleep(args.interval)

    with open(args.output, "w") as output, ThreadPoolExecutor(max_workers=args.workers + 1) as executor:
        futures = [executor.submit(worker, i, output) for i in range(args.workers + 1)]
        for future in futures:
            future.result()
    summary = {"url": args.url, "duration_seconds": args.seconds, "requests": len(rows),
               "errors": sum("error" in row for row in rows),
               "versions": dict(Counter(row.get("version", "error") for row in rows)),
               "instances": dict(Counter(row.get("instance", "error") for row in rows)),
               "slow_requests": sum(row["kind"] == "slow" for row in rows),
               "slow_errors": sum(row["kind"] == "slow" and "error" in row for row in rows)}
    normal = sorted(row["latency_ms"] for row in rows if row["kind"] == "normal" and "error" not in row)
    if normal:
        summary["normal_p95_ms"] = normal[min(len(normal)-1, int(len(normal)*0.95))]
        summary["normal_max_ms"] = normal[-1]
    print(json.dumps(summary, indent=2))
    Path(args.output + ".summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    raise SystemExit(1 if summary["errors"] else 0)


if __name__ == "__main__":
    main()
