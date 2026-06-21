# Performance Report

All numbers below come from `scripts/benchmark.sh`, run against the server on
`localhost:8080` with the full AOL dataset loaded (1.24M unique queries). The
script is reproducible: start the server, run the script, get these figures.

Measurement setup: 5000 requests per latency test at concurrency 50, using
`hey` (a standard HTTP load generator). Latency is measured end to end from the
client, so it includes HTTP and JSON serialization, not just internal compute.
This is deliberately the honest number a real client would see.

## Summary

| Metric | Result |
|---|---|
| p95 latency, cache-hit path | ~1.2 ms |
| p95 latency, trie path (cache bypassed) | ~1.2 ms |
| Cache hit rate (repeated-prefix traffic) | 99.9% |
| Write reduction through batching | 100x (1000 searches to 10 DB rows) |

## 1. Latency

Two paths were measured. The cache-hit path hammers a single hot prefix, so
after the first request every response is served from the cache. The trie path
uses trending mode, which bypasses the cache and computes live from the trie,
isolating the trie lookup and rerank cost.

```
cache-hit path:   p50 0.5ms   p95 1.2ms   p99 8.7ms
trie path:        p50 0.4ms   p95 1.2ms   p99 1.9ms
```

The interesting finding is that the two paths are essentially equal at p95. The
cache provides no latency benefit here. That is expected once you look at what
the trie actually does on a read: walk down the prefix (a handful of map
lookups) and return a precomputed top-10 list. That is already on the order of
microseconds, so there is nothing for the cache to speed up.

This does not mean the cache is pointless. Its value is architectural, not
local. The cache is the layer that distributes across nodes via consistent
hashing and that would absorb load if the underlying store were slow or remote
(a real database over the network, rather than an in-memory trie). On a single
machine with an in-memory index, the cache and the trie are simply both fast,
so they measure the same. The honest reading is: at this scale the cache earns
its place as a distribution and scaling mechanism, not as a local speedup.

One detail worth noting is the cache path's higher p99 (8.7ms vs the trie's
1.9ms). Each cache node is a single goroutine that serves requests one at a
time over a channel. Under 50 concurrent requests, requests for the same node
queue behind each other, and that shows up in the tail. The trie path, by
contrast, is a concurrent read guarded by a read-write mutex, so many reads
proceed in parallel and the tail stays tight. This is a real trade-off of the
channel-per-node design: it is clean and lock-free within a node, but it
serializes that node's work. At higher concurrency you would shard hot nodes or
let a node process reads concurrently.

## 2. Cache hit rate

Driving repeated popular prefixes (`goog`, `map`, `ebay`, `yaho`, 200 requests
each) produced:

```
node0: 398 hits / 2 misses   (size 2)
node1: 5200 hits / 1 miss    (size 1)
node2: 199 hits / 1 miss     (size 1)
aggregate: 99.9% (5797 / 5801)
```

The hit rate is very high, but that is by construction: the test repeats a small
set of prefixes, so after the first miss each prefix is cached and every
subsequent request hits. This is realistic for head queries (a few prefixes
account for most traffic) but optimistic for the long tail, where diverse,
rarely repeated prefixes would miss more often. The number to take away is that
the cache works correctly and that repeated prefixes are served from it; the
exact percentage is a function of the access pattern.

The per-node split also shows consistent hashing routing: node1 happened to own
the hottest prefixes and so did most of the work. Load is uneven across only
four distinct prefixes, which is the small-sample lumpiness consistent hashing
smooths out as the number of distinct keys grows.

## 3. Write reduction through batching

Submitting 1000 searches across only 5 distinct queries, then letting the buffer
flush:

```
searches received: 1000
db flushes:        2
rows written:      10
reduction:         100x
```

1000 search submissions resulted in 10 database rows, a 100x reduction. The
mechanism is aggregation: repeated queries are tallied in memory and collapsed
into a single increment per query per flush, so the database sees one write per
distinct query instead of one per search.

The reason it is 10 rows and not 5 is that the buffer flushed twice during the
run. The 5-second timer fired once partway through the 1000 sequential
submissions, so each of the 5 distinct queries was written once per flush, 10
rows total. Had all 1000 fit inside a single flush window, it would have been 5
rows, a 200x reduction. Either way the point holds: write volume to the durable
store is decoupled from search volume and scales with distinct queries per flush
interval, not with raw traffic.

## Trade-offs reflected in these numbers

- The cache trades memory and a small tail-latency cost (channel serialization)
  for a distribution layer that does not pay off locally but is the right shape
  at scale. Reported honestly: no local speedup, clear architectural purpose.
- Batching trades durability for write reduction. The 100x fewer writes come at
  the cost of losing whatever sits in the buffer if the process is hard-killed
  between flushes. Acceptable for ranking data; the clean-shutdown path flushes
  to shrink the window.
- The trie's per-node top-K trades write-time work (recomputing lists) for
  near-zero read cost, which is why read latency is so low. This suits a
  read-heavy workload where reads vastly outnumber writes.