# Search Typeahead System: Design Document

## 1. What this is

A search-as-you-type service. You start typing in a box, and a dropdown shows the most popular queries that start with what you typed, ordered by popularity. You can submit a search, which bumps that query's count and returns a stub response. The system also surfaces trending queries and keeps its writes off the hot path so reads stay fast.

The interesting part is not the feature list. It is one fact that shapes every decision below: reads massively outnumber writes. Every keystroke is a read. A search submission, by comparison, is rare. So the whole system is built to make reads close to free and to push all the cost onto the infrequent write side, done lazily. If you remember nothing else from this document, remember that. Almost every choice here is a consequence of it.

## 2. Dataset

I am using the AOL query log (the Kaggle "AOL User Session Collection" version). It is about 20 million real search events from roughly 650k users over three months in 2006. It is the only large public dataset that is actually raw human search queries with enough volume to look real.

The raw log is one row per search event, with user IDs, timestamps, and clicked URLs. I only want the query text, so ingestion strips everything else. A note on the AOL log: it was withdrawn in 2006 over a privacy problem, because the user-level data could be de-anonymized. That does not affect this project, because I discard every user-identifying column and keep only the query string. The README says this explicitly.

The assignment wants a `query, count` table. The raw log does not have counts, it has events, so I derive counts by aggregation: group by the normalized query, count the rows. That gives me something like:

```
query              count
iphone             100000
iphone 15          85000
iphone charger     60000
java tutorial      40000
```

After aggregation one log file (~3.6M raw search events) yields about 1.24M unique queries, which clears the assignment's 100k minimum comfortably. Normalization also drops the `-` placeholder that the AOL log uses for empty queries; left in, it would sit at the top of the rankings as noise.

## 3. Storage: why there are four layers

This is the part most likely to get questioned, so here is the reasoning up front. There are four places data lives, and each one has a different job. They are not redundant.

**Postgres is the source of truth.** It holds the canonical `query, count` table. It is durable, so it survives a restart. Batched writes land here. If the process dies, this is what I reload from. Postgres exists to make the counts persist.

**The trie is an in-memory index built from Postgres.** At startup I read the whole `query, count` table and insert every query into a trie. The trie is what actually answers "give me everything starting with `ip`, top 10 by count." It is fast, and it is volatile. It lives in RAM and dies with the process, which is fine, because Postgres can always rebuild it.

**The distributed cache holds finished answers for hot prefixes.** It maps a prefix to its top-10 list. It sits in front of the trie. Most reads never reach the trie at all because the cache already has the answer.

**The write buffer holds recent search submissions in memory.** It tallies them and flushes to Postgres in bulk on a timer or when it fills up.

The split between Postgres and the trie is the one worth being able to defend in a sentence: Postgres is the durable truth, the trie is a fast volatile index derived from it. Durability and read speed are different jobs, so they get different structures. One honest consequence: the trie and Postgres can be briefly out of sync between a write and the next flush. That is acceptable here because this is ranking data. Counts that are approximate for a few seconds do not change which suggestions show up.

## 4. The read path

This runs on every keystroke, so it has to be cheap.

### 4.1 The data structure: trie

The job is "find everything starting with a prefix, fast." A flat list means checking every query on every keystroke, which does not scale. A sorted list is better, you can binary-search to the block of matches, but it is painful to keep sorted as queries get added, and it does not help with ranking by count.

A trie is the right fit. It is a tree of letters where words sharing a beginning share a path. To find everything under `ip`, you walk `i` then `p` and grab the subtree below. The cost is the length of the prefix, not the size of the dataset. Whether there are a hundred queries or a hundred million, walking to `p` is two steps.

I will be honest about scale here: at 100k rows running locally, a sorted list would also be fast enough. The trie wins because it handles inserts cleanly (a new query just adds a few nodes, nothing shifts) and because it sets up the ranking trick below. It is the correct structure at real scale, and that is the story, not that the demo would otherwise crawl.

### 4.2 Ranking: per-node top-10

Finding matches is half the problem. The other half is returning the *ten most popular* matches, sorted. The lazy way is to scan all matches and sort them on every keystroke. For a broad prefix like `i`, that is thousands of entries scanned per request.

Instead, each trie node caches its own top 10. The `ip` node already knows its ten most popular descendants. A read becomes: walk to the node, read the stored list, done. No scanning, no sorting at read time, regardless of how many queries sit below.

The cost moves to write time. When a count changes, the top-10 lists of every prefix of that query may need updating. That is the trade: reads get cheap, writes get heavier. Given that reads dominate and writes are batched anyway (section 6), this is the right direction to push the cost.

I store the full top-10 list at each node rather than a more memory-efficient scheme that rebuilds on read. Memory is not the constraint here, read latency is, so spending memory to keep reads flat is the correct call. If memory ever became the bottleneck, the upgrade is to cache only the shallow, high-traffic nodes and recompute the deep, rare ones live, since short prefixes are both expensive to compute and hit constantly, while deep prefixes are cheap and rarely touched.

### 4.3 The cache in front

Even with a fast trie, I do not want to walk it on every single request. A cache layer sits in front, mapping prefix to its finished top-10 list. Read flow:

1. Hash the prefix, pick the cache node that owns it (section 5).
2. Ask that node. Hit means return the stored list instantly, and the trie is never touched.
3. Miss means go to the trie, compute the top 10, store it in the cache with a TTL, return it.

Entries leave the cache for two reasons. They go stale, handled by a TTL: each entry is good for a short window, then expires and gets recomputed on the next miss. Or the cache fills up, handled by LRU eviction: when full, drop the least recently used entry.

I use TTL as the main freshness mechanism rather than active invalidation. Search suggestions tolerate being a few seconds out of date, nobody notices, and TTL needs no bookkeeping. Active invalidation is more precise but more complex, and the precision is not worth it for ranking suggestions.

## 5. Distributing the cache: consistent hashing

The assignment requires the cache to be split across several nodes, with consistent hashing deciding which node owns which prefix.

First, the honest framing: at this scale a single in-memory cache would be plenty. The dataset fits in memory many times over, traffic is low, and there is no real availability requirement. The reason to distribute a cache in the real world is capacity (data outgrows one machine's RAM), throughput (one machine cannot serve the request rate), and fault tolerance (one machine dying should not wipe the whole cache). None of those bite here. I build it distributed to demonstrate the pattern on a system small enough to actually observe. The nodes are logical: each node is its own goroutine that owns a private map, and you talk to it by sending request messages over a channel and reading the reply off a reply channel. Because only that one goroutine ever touches the map, there is no shared state and no mutex is needed within a node. The channel is the stand-in for the network boundary; in production each node would be a separate process or a Redis instance, and the routing below would be identical.

The naive way to pick a node is `hash(prefix) % N`. It works until N changes. Add or remove a node and the divisor changes, so almost every key remaps to a different node at once. The entire cache goes cold simultaneously and every request stampedes the trie. That is the failure consistent hashing exists to prevent.

Consistent hashing puts both nodes and keys on a ring of hash values. Each key is owned by the first node clockwise from it. When a node is added or removed, only the keys in its arc move. Everything else stays put. So a node change remaps roughly 1/N of keys instead of nearly all of them.

One refinement: virtual nodes. If each physical node sits at a single ring position, the arcs can be wildly uneven and one node gets overloaded. So each physical node is placed at many ring positions. That evens out the load and, when a node dies, scatters its keys across many nodes instead of dumping them all on one neighbor.

The `GET /cache/debug?prefix=` endpoint exists to make this visible: it reports which node owns a prefix and whether it was a hit or miss. The demo shows the remapping behavior directly: which node owns prefix X, then add a node, then show that only a few keys moved.

## 6. Batch writes

Writing to Postgres on every single search submission would hammer it with tiny writes and make it the bottleneck. So submissions do not write through synchronously.

Instead they land in an in-memory buffer that tallies them. Three searches for `iphone` do not become three writes, they become one "add 3 to iphone." Collapsing repeats is the real win, not just the batching. A query searched a thousand times in one window becomes a single `+1000`.

The buffer flushes on whichever comes first: a timer (every few seconds) or a size threshold (the buffer reaches some count). The timer guarantees data does not sit forever, the size cap guarantees the buffer does not grow without bound during a spike.

The cost is durability. If the process crashes with searches still in the buffer, those increments are lost, because they only ever lived in memory. I accept that, because this is search-count data for ranking. A few lost increments out of a hundred thousand do not change which suggestions appear. The write reduction is large and the data tolerates small loss, so it is a good trade here. If this were money or orders, I would choose the opposite, and that context-dependence is the point.

If I needed to shrink the loss, the options are a write-ahead log (append each search to disk before buffering, replay on restart) or a shorter flush interval (smaller loss window, but you give back some of the batching benefit). Durability and write-reduction trade off directly along that spectrum. For this app, simple in-memory buffering is the right point on it.

The demo logs the before-and-after: N search submissions producing far fewer actual database writes after aggregation. That number is the evidence the assignment asks for.

## 7. Trending and recency

The basic ranking is by all-time count, which earns the bulk of the marks. The upgrade, worth the bonus, is recency: things searched recently should rank higher, even if their all-time count is modest. A query can blow up today while still trailing an all-time giant like `iphone`, and pure all-time ranking would keep it buried.

The trap, which the assignment calls out explicitly, is that a brief spike must not rank highly forever. That kills the obvious fix of `score = count + recent_count`, because the recent bonus would be permanent. The recency signal has to fade.

I use exponential time decay. Each query keeps one decaying recency score plus the time it was last touched. On a new search, the stored score is first decayed forward to now, then the new boost is added:

```
dt    = now - last_updated
score = score * exp(-lambda * dt) + boost
last_updated = now
```

When ranking, the score is decayed forward to now and read (without adding a boost). The final ranking score combines both signals:

```
final_score = all_time_count + w * recency_score
```

Two things worth explaining about this. First, the boost is just a fixed unit per search (use 1). Its absolute value does not matter, because the weight `w` rescales the whole recency term in the final score. So the real tuning lives in `w`, not the boost.

Second, the reason for exponential decay specifically, and not some other fade curve, is that exponential decay is composable. Decaying once across a two-minute gap gives exactly the same result as decaying minute by minute. That property is the entire reason I can store one number per query instead of a list of every search event. A different curve would break the shortcut and force me to keep history. So the formula is chosen because it enables the data structure, not for its own sake.

This gives two tuning knobs:

- The decay rate `lambda` (equivalently, a half-life): how fast trends fade. Short half-life is reactive and jumpy, long is sluggish and stable.
- The recency weight `w`: how far a hot trend can climb over an all-time favorite. Too high and noise dominates, too low and recency barely shows.

Because rankings now change with time and not just with new searches, cached suggestion lists would go stale fast. Rather than fight that, the trending path bypasses the cache entirely: `/suggest?q=<prefix>&mode=trending` computes live from the trie and reranks by recency on each request, while the default `/suggest` stays cached and count-ranked. This keeps the basic path fast and cached, isolates the recency cost to the requests that ask for it, and directly demonstrates the difference between the two ranking approaches that the assignment asks for. The trade-off is explicit: the trending path does more work per request and is not cached, because caching a continuously-decaying order would serve stale rankings. That is the freshness-versus-latency choice, made deliberately by separating the two paths rather than forcing recency through the cache.

The same `GET /suggest` endpoint serves both the basic and the enhanced ranking. The demo shows the difference with logs: rank by all-time count, inject a burst for some query, watch it climb, let time pass, watch it fall back. The falling-back is the proof that brief spikes do not persist.

The five things the assignment asks me to explain map directly onto the above. Recent searches are tracked as one decaying score per query. Recent activity affects ranking through the weighted recency term. Brief spikes do not over-rank permanently because the term decays toward zero once activity stops. The cache is kept correct by having the trending path bypass it (the basic cached path is unaffected by recency). The trade-offs are freshness against latency (shorter TTL is fresher but more recompute) and freshness against complexity (all-time count is trivial, decay is more moving parts), plus the window-versus-decay choice, where I picked decay for its smooth fade and the single-number storage trick.

## 8. APIs

```
GET  /suggest?q=<prefix>        Returns up to 10 prefix matches, sorted by score.
POST /search                    Records a submission, returns {"message": "Searched"}.
GET  /cache/debug?prefix=<p>    Shows which cache node owns the prefix and hit/miss.
```

`/suggest` is the hot read path: cache first, trie on miss. `/search` is the cold write path: return the stub immediately, then record the search into the write buffer and update the recency score, without blocking on any database work. `/cache/debug` exists purely to make consistent-hashing behavior observable for the demo and the report.

## 9. Request flows end to end

**Read (user types `ip`):**

1. Frontend debounces, then calls `GET /suggest?q=ip`.
2. Backend hashes `ip` on the ring, picks the owning cache node.
3. Cache hit returns the stored top 10 instantly.
4. Cache miss walks the trie (`i`, `p`, read the per-node top 10, blended with recency), stores the result in the cache with a TTL, and returns it.

**Write (user submits `iphone`):**

1. Frontend calls `POST /search` with `iphone`.
2. Backend returns `{"message": "Searched"}` right away. The user never waits on the database.
3. Backend records the search into the write buffer (tally +1) and updates the recency score for `iphone`.
4. Later, on a flush trigger, the buffer aggregates and bulk-writes to Postgres.
5. Affected cache entries expire via TTL and get recomputed on the next miss.

## 10. Non-functional notes

- The system runs locally. Postgres in Docker on host port 5433 (to avoid colliding with a local Postgres install), the Go server as a single process holding the trie, cache nodes, write buffer, and ring.
- The read path is optimized for low latency. Measured p95 is about 1.2ms (see PERFORMANCE.md). Notably, the cache and trie paths measure the same locally: the trie's per-node top-K is already so fast that the cache adds no local speedup, so the cache's value here is purely as a distribution layer, an honest finding rather than a claimed win.
- Cache hit rate and the database write count (before and after batching) are reported via `/cache/stats` and `/stats`; batching showed a 100x write reduction in the benchmark.
- Consistent-hashing behavior is shown through the debug endpoint, the `ringtest` utility, and logs.
- Code is split by concern: trie, cache plus ring, write buffer, trending scorer, HTTP handlers (in an `api` package), ingestion.

## 11. What I would do differently at real scale

The honest version of this section: most of the distribution here is pedagogical. At real scale the cache nodes would be separate processes or separate machines (Redis instances, say), the trie would likely be sharded or replaced with a purpose-built suggestion service, and the write buffer would be a real queue or log with durability guarantees. The designs above are the right shapes for those, but the local demo simulates them in one process. I would rather state that plainly than pretend a single-machine demo needs distributed infrastructure. The value of the exercise is understanding why each pattern exists and what it costs, which is exactly what the viva is checking.