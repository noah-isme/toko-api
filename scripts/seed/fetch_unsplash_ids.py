#!/usr/bin/env python3
"""Resolve real images.unsplash.com photo ids for product queries.

Queries a local SearXNG instance in image mode and keeps the results that are
already hosted on images.unsplash.com, so no page scrape or Unsplash API key is
needed. Every candidate is fetched before being accepted, which keeps dead ids
(Unsplash does retire photos) out of the seed migration.

The previous approach searched for unsplash.com/photos pages and followed the
/download redirect to learn the canonical id. Upstream engines rate-limit that
path so aggressively that most queries came back empty; image mode returns the
canonical URL directly.

Usage: python3 fetch_unsplash_ids.py queries.json [want]
"""
import json
import os
import re
import subprocess
import sys
import time

SEARX = os.environ.get("SEARX_URL", "http://localhost:8080/search")
HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "unsplash_ids.json")

PHOTO_ID = re.compile(r"images\.unsplash\.com/(photo-\d{10,13}-[0-9a-f]{9,14})")
GALLERY_PARAMS = "?auto=format&fit=crop&w=1200&q=80"


def curl(args, timeout=25):
    try:
        done = subprocess.run(
            ["curl", "-s", "--max-time", str(timeout), *args],
            capture_output=True, text=True, timeout=timeout + 10,
        )
        return done.stdout
    except subprocess.TimeoutExpired:
        return ""


def search_ids(query):
    """Return unsplash photo ids the image search surfaced, best matches first."""
    body = curl(["-A", "Mozilla/5.0", SEARX,
                 "--data-urlencode", f"q={query}",
                 "-d", "format=json", "-d", "categories=images"])
    try:
        payload = json.loads(body)
    except ValueError:
        return []

    found = []
    for result in payload.get("results", []):
        for field in ("img_src", "thumbnail_src", "url", "source"):
            match = PHOTO_ID.search(result.get(field) or "")
            if match and match.group(1) not in found:
                found.append(match.group(1))
                break
    return found


def reachable(photo_id):
    code = curl(["-o", os.devnull, "-w", "%{http_code}",
                 f"https://images.unsplash.com/{photo_id}{GALLERY_PARAMS}"], timeout=15)
    return code.strip() == "200"


def main():
    queries_path = sys.argv[1] if len(sys.argv) > 1 else os.path.join(HERE, "queries.json")
    want = int(sys.argv[2]) if len(sys.argv) > 2 else 3
    queries = json.load(open(queries_path))

    result = {}
    if os.path.exists(OUT):
        result = json.load(open(OUT))

    total = len(queries)
    for index, query in enumerate(queries, 1):
        ids = list(result.get(query, []))
        if len(ids) < want:
            # Engines suspend individual backends under load, so retry the same
            # query a few times with a pause rather than dropping the product.
            for attempt in range(3):
                for candidate in search_ids(query):
                    if len(ids) >= want:
                        break
                    if candidate in ids:
                        continue
                    if reachable(candidate):
                        ids.append(candidate)
                if len(ids) >= want:
                    break
                time.sleep(5)
            result[query] = ids
            json.dump(result, open(OUT, "w"), indent=1)

        progress = {"current": index, "total": total, "unit": "queries",
                    "message": f"{query}: {len(ids)} ids"}
        print(f"JCODE_PROGRESS {json.dumps(progress)}", flush=True)

    missing = [q for q, v in result.items() if len(v) < want]
    print(f"done. under-filled: {len(missing)} -> {missing[:10]}")


if __name__ == "__main__":
    main()
