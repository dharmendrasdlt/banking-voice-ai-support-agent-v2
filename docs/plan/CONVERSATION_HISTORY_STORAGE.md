# Conversation-History Storage

Where conversation history lives — and why it is **not** the banking store.

Conversation history is an **append-only, immutable, write-heavy, ever-growing**
event log (billions of records at 10M users), read mostly for audit/QA and
occasional per-user lookups, and it is **PII-laden with regulated retention**.
That profile does not match an operational document DB — so it is kept separate
from the banking MongoDB (which is the small, hot, rich-query financial
source-of-truth).

---

## Two layers, two stores

| Layer | Store | Role |
|---|---|---|
| **Working context** (last N turns for the LLM prompt) | **Redis** (`session:{id}:context`, TTL, purge on call-end) | hot, low-latency, ephemeral |
| **Durable history** (full transcripts, audit, per-user lookups) | **Cassandra** (production) | append-heavy, retained, operational reads |

The durable store does **not** serve the low-latency turn — that's Redis. So it
only needs cheap high-volume writes + occasional reads.

## Why Cassandra (production)

- **Write-optimized, append-heavy** — matches the workload; massive write
  throughput, horizontally scalable, no single write bottleneck.
- **Per-user / per-session point + range lookups at scale** — "fetch this user's
  conversation, ordered by time" is a single-partition read.
- **Per-row TTL** — retention policies (auto-expiry) fall out naturally.
- The classic "chat/message history at scale" choice.
- **ScyllaDB** is a drop-in, C\*-compatible alternative (C++ rewrite) — lower
  latency and ops cost; use it if you want better perf/economics.

### Data model (partition + clustering)

```
PRIMARY KEY ((user_id), conversation_id, turn_seq)
  partition key : user_id            -- all of a user's history co-located
  clustering    : conversation_id, turn_seq (time-ordered)
  columns       : ts, role, transcript, intent, action, result, ...
  TTL           : per retention policy
```

This gives efficient "all conversations for user U" and "conversation X in order."

## The caveat — Cassandra is the *operational* tier

Cassandra is great for point/range reads by partition. Long-term analytics or compliance archiving can be added in the future if required, but for the hot operational path, Cassandra is the single sufficient store.

Production shape: **Orchestrator → Kafka → consumer → Cassandra** (operational history).

## Local scaffold

- **Default:** keep it light — **Redis** working context + **Redis Streams** as
  the Kafka stand-in. No Cassandra needed to validate the flow.
- **Optional:** run a **single-node ScyllaDB** locally to exercise the
  `Kafka(stand-in) → consumer → Cassandra` drain path. Not run by default (heavy
  on an M3 Pro).
- **Never** put conversation history in the banking MongoDB.

## Summary

- Working context = Redis; durable history = **Cassandra** (production),
  fed by the Kafka consumer.
- Conversation history is a different domain from banking data — keep it out of
  the banking Mongo.
