#!/usr/bin/env bash
#
# benchmark.sh — reproducible performance measurement for the typeahead system.
#
# Measures the three things the assignment's performance report needs:
#   1. p95 latency on /suggest (cache HIT path vs cache MISS path)
#   2. cache hit rate
#   3. write reduction through batching
#
# Prerequisites:
#   - server running on :8080  (go run ./cmd/server)
#   - `hey` installed          (brew install hey)
#   - jq installed for pretty output (brew install jq) — optional
#
# Usage:  ./scripts/benchmark.sh
#
set -euo pipefail

BASE="http://localhost:8080"
N=5000      # total requests per latency test
C=50        # concurrency

line() { printf '%s\n' "------------------------------------------------------------"; }

echo
echo "TYPEAHEAD PERFORMANCE BENCHMARK"
echo "requests per test: $N   concurrency: $C"
line

# ---------------------------------------------------------------------------
# 1. LATENCY — CACHE HIT PATH
# Hammer ONE prefix repeatedly. After the first request it's cached, so almost
# every request is a cache hit. This shows best-case served-from-cache latency.
# ---------------------------------------------------------------------------
echo
echo "[1/4] Latency — cache HIT path (single hot prefix 'goog')"
echo

# warm the cache once so the very first measured request is already a hit
curl -s "$BASE/suggest?q=goog" > /dev/null

hey -n "$N" -c "$C" "$BASE/suggest?q=goog" \
  | grep -A 8 "Latency distribution"
line

# ---------------------------------------------------------------------------
# 2. LATENCY — CACHE MISS / TRIE PATH
# Use trending mode, which bypasses the cache and computes live from the trie.
# This isolates the trie+rerank cost (the worst case, no cache help).
# ---------------------------------------------------------------------------
echo
echo "[2/4] Latency — trie path, cache bypassed (trending mode)"
echo

hey -n "$N" -c "$C" "$BASE/suggest?q=goog&mode=trending" \
  | grep -A 8 "Latency distribution"
line

# ---------------------------------------------------------------------------
# 3. CACHE HIT RATE
# Drive a realistic mix: a few popular prefixes hit repeatedly (cache hits)
# plus some one-off prefixes (cache misses). Then read /cache/stats.
# ---------------------------------------------------------------------------
echo
echo "[3/4] Cache hit rate (mixed traffic)"
echo

# popular prefixes, hit many times each -> should be cache hits after first
for p in goog map ebay yaho; do
  for i in $(seq 1 200); do curl -s "$BASE/suggest?q=$p" > /dev/null; done
done

echo "per-node cache stats:"
curl -s "$BASE/cache/stats"
echo
echo
# compute aggregate hit rate with awk over the JSON (no jq dependency)
curl -s "$BASE/cache/stats" | tr ',' '\n' | grep -o '"hits":[0-9]*\|"misses":[0-9]*' \
  | awk -F: '/hits/{h+=$2} /misses/{m+=$2} END{
      if (h+m>0) printf "aggregate hit rate: %.1f%% (%d hits / %d total)\n", 100*h/(h+m), h, h+m;
      else print "no cache traffic recorded";
    }'
line

# ---------------------------------------------------------------------------
# 4. WRITE REDUCTION THROUGH BATCHING
# Fire many search submissions, heavily repeating a few queries. The buffer
# aggregates repeats, so DB rows written << searches received.
# ---------------------------------------------------------------------------
echo
echo "[4/4] Write reduction through batching"
echo

SEARCHES=1000
echo "submitting $SEARCHES searches across 5 repeated queries..."
for i in $(seq 1 "$SEARCHES"); do
  # cycle through 5 queries so repeats aggregate
  case $((i % 5)) in
    0) Q="iphone";;
    1) Q="ipad";;
    2) Q="macbook";;
    3) Q="airpods";;
    *) Q="ipod";;
  esac
  curl -s -X POST "$BASE/search" -d "{\"query\":\"$Q\"}" > /dev/null
done

echo "waiting 6s for the buffer to flush..."
sleep 6

echo "write-buffer stats:"
curl -s "$BASE/stats"
echo
echo
curl -s "$BASE/stats" | tr ',' '\n' | grep -o '"searches_received":[0-9]*\|"rows_written":[0-9]*' \
  | awk -F: '/searches_received/{s=$2} /rows_written/{r=$2} END{
      if (r>0) printf "write reduction: %d searches -> %d db rows  (%.1fx fewer writes)\n", s, r, s/r;
    }'
line
echo
echo "done."
echo