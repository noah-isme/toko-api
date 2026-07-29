#!/usr/bin/env python3
"""Resolve real images.unsplash.com photo ids for product queries.

Uses a local SearXNG instance to find unsplash.com/photos/<slug> pages, then
follows the /download redirect which exposes the canonical photo-<id> path.
"""
import json
import os
import re
import subprocess
import sys
import time

SEARX = "http://localhost:8080/search"
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "unsplash_ids.json")

PHOTO_PAGE = re.compile(r"unsplash\.com/photos/([A-Za-z0-9_-]{6,})")
PHOTO_ID = re.compile(r"images\.unsplash\.com/(photo-\d{10,13}-[0-9a-f]{9,14})")


def curl(args, timeout=25):
    try:
        return subprocess.run(
            ["curl", "-s", "--max-time", str(timeout), *args],
            capture_output=True, text=True, timeout=timeout + 10,
        ).stdout
    except subprocess.TimeoutExpired:
        return ""


def search_pages(query, want):
    # Upstream engines rate-limit aggressively, so back off between attempts and
    # try progressively looser query forms before giving up on a product.
    forms = [
        f"site:unsplash.com/photos {query}",
        f"unsplash photo {query}",
        f"unsplash {query.split()[0]} photo",
    ]
    slugs = []
    for attempt in range(len(forms) * 3):
        body = curl(["-A", "Mozilla/5.0", SEARX,
                     "--data-urlencode", f"q={forms[attempt % len(forms)]}",
                     "-d", "format=json"])
        for m in PHOTO_PAGE.finditer(body):
            # Page slugs look like "white-nike-air-force-1-kcZtpgTm0og"; only the
            # trailing token is the photo id the download endpoint accepts.
            short = m.group(1).rsplit("-", 1)[-1]
            if len(short) < 8 or short in slugs:
                continue
            slugs.append(short)
        if len(slugs) >= want * 2:
            break
        time.sleep(6)
    return slugs


def resolve(slug):
    head = curl(["-D", "-", "-o", "/dev/null", "-A", "Mozilla/5.0",
                 f"https://unsplash.com/photos/{slug}/download?force=true"])
    m = PHOTO_ID.search(head)
    return m.group(1) if m else None


def main():
    queries = json.load(open(sys.argv[1]))
    want = int(sys.argv[2]) if len(sys.argv) > 2 else 3
    result = {}
    if os.path.exists(OUT):
        result = json.load(open(OUT))
    total = len(queries)
    for i, q in enumerate(queries, 1):
        if len(result.get(q, [])) >= want:
            continue
        ids = list(result.get(q, []))
        for slug in search_pages(q, want):
            if len(ids) >= want:
                break
            pid = resolve(slug)
            if pid and pid not in ids:
                ids.append(pid)
            time.sleep(0.2)
        result[q] = ids
        json.dump(result, open(OUT, "w"), indent=1)
        print(f"JCODE_PROGRESS {json.dumps({'current': i, 'total': total, 'unit': 'queries', 'message': f'{q}: {len(ids)} ids'})}", flush=True)
    missing = [q for q, v in result.items() if len(v) < want]
    print(f"done. under-filled: {len(missing)} -> {missing[:10]}")


if __name__ == "__main__":
    main()
