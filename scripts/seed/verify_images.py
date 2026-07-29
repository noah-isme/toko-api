#!/usr/bin/env python3
"""Check every image URL in the generated seed migration returns HTTP 200.

Run after gen_seed_sql.py so a broken Unsplash id never reaches a database.

Usage: python3 scripts/seed/verify_images.py
"""
import os
import re
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor

HERE = os.path.dirname(os.path.abspath(__file__))
UP = os.path.normpath(os.path.join(HERE, "..", "..", "migrations",
                                   "000025_full_seed_catalog.up.sql"))
URL = re.compile(r"https://images\.unsplash\.com/[^']+")


def status(url):
    proc = subprocess.run(
        ["curl", "-s", "-o", os.devnull, "-w", "%{http_code}", "--max-time", "20",
         "-r", "0-0", url],
        capture_output=True, text=True,
    )
    return url, proc.stdout.strip()


def main():
    sql = open(UP).read()
    urls = sorted(set(URL.findall(sql)))
    bad = []
    with ThreadPoolExecutor(max_workers=8) as pool:
        for i, (url, code) in enumerate(pool.map(status, urls), 1):
            if code not in ("200", "206"):
                bad.append((url, code))
            if i % 10 == 0 or i == len(urls):
                print(f"checked {i}/{len(urls)} urls, {len(bad)} bad", flush=True)
    for url, code in bad:
        print(f"BAD {code} {url}")
    print(f"{len(urls) - len(bad)}/{len(urls)} image urls OK")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
