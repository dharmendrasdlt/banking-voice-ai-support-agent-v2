# Failover Design

How the system survives an instance crashing mid-conversation without dropping
the customer or corrupting state. This is a **production-scale** concern — the
local scaffold is single-instance and does not implement any of it — but the
reasoning is recorded so it isn't lost.

Two tiers fail very differently:

| Tier | Holds customer connection? | State | Failure character |
|---|---|---|---|
| **Media Engine** (Part A) | **Yes** (WebRTC/gRPC) | in-flight audio | Hard — the customer connection drops; must reconnect + recover in-flight audio |
| **Orchestrator** (Part B) | No — it's a backend | **stateless** (all state in Redis) | Easy availability (re-route to a healthy instance) but hard **correctness** (it performs side effects mid-turn) |

See `production-architecture.drawio.svg` for the topology. This is
**production reference only** — do not build LBs/clusters/failover in the local
scaffold.

---

# Part A — Media-Engine Failover

## A1. The problem

Audio flows: `customer → Media Engine (VAD) → STT → transcript → orchestrator /
session store`. Each customer holds a **stateful live connection** to one
media-engine instance. If it dies mid-call, two things are at risk:

- **Conversation state** (turn, dialog state, completed transcripts, identity).
- **In-flight audio** (the current, not-yet-transcribed utterance).

## A2. Two things must survive — treat them differently

| | Volume | Importance | Where it lives |
|---|---|---|---|
| **Session state** | Low | **Essential** | Session ID store (Redis KV) + history via Kafka |
| **In-flight audio** | High | Marginal — only the *current* utterance | Bounded window in the Redis stream (or dropped) |

Completed utterances are already transcribed; their audio is never needed again.
So the Redis stream only holds a **short rolling window of the current utterance
per session** — never the whole call's audio.

**Principle: persist the *understanding* durably; treat raw in-flight audio as
best-effort.**

## A3. Where loss actually happens

Loss is bounded by the **last durably-persisted checkpoint**, not by utterance
boundaries. At risk on crash: (1) chunks in ME memory not yet buffered/forwarded,
(2) in-flight STT requests whose transcript returns to a dead ME, (3) transcripts
computed but not yet written. Even the stream doesn't fully close #1 — chunks are
`XADD`'d fire-and-forget, so a crash between *receiving* and *writing* a chunk
loses it.

**Durability vs. latency:** synchronous durable write = ~zero loss but +latency on
the live path; async fire-and-forget = tiny loss window but fast. We accept the
small async window. Zero-loss is unreachable anyway once the transport reconnect
gap (A5) is added.

## A4. What helps (priority order)

1. **STT as a separate service + persist its output** — the *understanding*
   survives ME death even when raw audio doesn't (closes #2, #3).
2. **Checkpoint at the VAD utterance boundary** — resume at the last completed
   utterance; re-prompt the in-progress one ("sorry, you were saying?"). Cheap.
3. **Optional bounded rolling audio window** — avoids even the re-prompt, at the
   volume cost in A6.

Goal: worst case lose **a few seconds of raw in-flight audio**, never conversation
state or a completed transcript.

## A5. The transport reality — client connects directly to the Media Engine

The client holds a stateful connection directly to a Media Engine instance. When that instance dies, the connection is lost and the client must reconnect to a new healthy instance. There is no middle proxy hiding connection loss. Transparency comes from fast client-side reconnection rather than server-side session migration.

## A6. Why the Redis stream is scoped tightly (volume math)

At 1M concurrent: PCM 16 kHz/16-bit ≈ 32 KB/s/call → **~32 GB/s**; Opus (~24 kbps)
≈ 3 KB/s/call → **~3 GB/s**. Huge load for a buffer used on a rare crash. So bound
it to a **short rolling current-utterance window** (compressed), or push buffering
to the **client** (resumable upload). **Not** the whole-call audio store, and
**not** the audit log (that's Kafka).

---

# Part B — Orchestrator Failover

The orchestrator is central *logically* but is a **backend service** — which makes
its availability easy and its **correctness** the real work.

## B1. Why "central" ≠ single point of failure

- The **customer connection terminates at the Media Engine**, not the
  orchestrator. When an orchestrator instance dies, the customer's transport
  **does not drop** — the media engine retries its next call against a
  **healthy** orchestrator instance. Failover is a backend re-route, potentially
  invisible to the customer.
- The orchestrator is **stateless by mandate** (all durable state in Redis /
  session store). Any instance is interchangeable; the new one reloads session
  state and continues. *This is why statelessness is mandated.*
- Run **many instances** → redundancy removes the SPOF.

So availability = statelessness + redundancy + backend re-route. The hard part is
what the dying instance was *doing*.

## B2. In-flight turn recovery — by side-effect type

| What it was doing | Recovery | Risk |
|---|---|---|
| Cache lookup / embedding | retry — idempotent | none |
| LLM generation (tokens → TTS) | regenerate / resume | wasted compute; partial output already spoken (B4) |
| **Read** bank action (balance) | retry — idempotent | none |
| **Write** bank action (transfer, payment) | **never auto-retry** | double-execution — the dangerous one |

## B3. Money-movement rule (the important one)

Writes are the whole ballgame. On failover you usually **don't know whether the
write executed** — so "just tell the customer it failed" is a trap: if it actually
succeeded and you tell them to retry, **they** cause the double-transfer.

The rule:

> **Money-movement writes are never auto-retried.** On failover, resolve the
> outcome via an idempotent **status check** (keyed by an idempotency /
> transaction reference minted and persisted *before* dispatch), then tell the
> customer the truth:
> - **Committed** → confirm.
> - **Definitely not committed** → cleanly re-attempt, or report it didn't go
>   through.
> - **Indeterminate** (bank unreachable / no record) → **escalate to a human** to
>   verify against the ledger. **Never instruct the customer to "try again"** —
>   that relocates the double-execution risk to them.

**Reframe the idempotency key:** its job here is **reconciliation ("did it
happen?"), not safe auto-retry.** It's what lets you speak the truth to the
customer.

## B4. Announce success only after the action confirms

Partial TTS audio already spoken can't be un-said. So **never stream "your
transfer is done" before the bank call returns and is recorded.** Then a mid-action
failover can re-check and complete-or-apologize without having lied.

## B5. Checkpoint the turn (saga)

Model the turn as a small state machine checkpointed to Redis —
`{ intent, confirmed?, action_key, action_status, output_committed? }` — so the
recovering instance resumes from the last checkpoint instead of restarting blindly
or re-acting.

---

# Part C — STT / TTS Failover

STT and TTS are the **benign** tier: **pure, stateless model services with no side
effects.** Two consequences:

- **Correctness is trivial** — they're functions (audio→text, text→audio).
  Re-running them is always safe/idempotent; there is nothing to double-execute.
- **Neither holds the customer connection** (the Media Engine does), so a crash is
  a **backend re-route**, not a dropped call.

The only work is the small in-flight recompute and **resuming at the right
position**.

## C1. STT instance fails

Streaming STT holds **per-utterance decoder context** (the audio-so-far for the
current utterance). If it dies mid-utterance that context is lost, but recovery is
clean:

- **Re-route** to a healthy STT instance and **replay the current-utterance
  audio** to rebuild context — this **reuses the Media Engine's rolling
  current-utterance buffer** (Part A2), no new mechanism.
- No buffer → the current utterance's partial transcription is lost → **re-prompt**
  ("sorry, could you repeat that?").
- Completed utterances are already transcribed and persisted, so you only ever
  replay the **current** one. Bounded. Pure function → replay is harmless.

## C2. TTS instance fails

TTS is synthesizing the response text into audio the customer is already hearing.
If it dies mid-response:

- The input (**the response text**) is known → **re-synthesize the un-spoken
  remainder** on a healthy instance.
- **Do not restart from the top** — the customer already heard the first half.
  **Resume at the last delivered sentence boundary** (sentence-boundary chunking
  makes this clean; a few repeated words are tolerable).
- Requires **retaining the response text until playback completes** and **tracking
  playback position**.

## C3. Because there are no side effects, you can be aggressive

Unlike money-movement writes, STT/TTS re-execution is free of correctness risk —
so you may use **fast retries and even hedged requests** (fire to two instances,
take the first). This is the clean contrast to Part B's "never retry a write."

## C4. What must be retained (resilience state, all tiers)

| Component | Retain until… | For recovery |
|---|---|---|
| Media Engine | current utterance ends | replay audio |
| STT | final transcript produced | replay current-utterance audio to a new instance |
| TTS | playback of the response completes | re-synthesize the un-spoken remainder |
| Orchestrator | action result reconciled | resume from turn checkpoint (Part B5) |

---

## Local scaffold vs. production (all tiers)

- **Local scaffold:** single instance, no failover — none of Parts A/B/C is
  implemented. Redis Streams are only a local stand-in for the async/durable log.
- **Build in the scaffold anyway (cheap, load-bearing later):**
  1. **Statelessness** — keep orchestrator state out of memory (Redis only).
  2. **Idempotency keys on mutating bank actions** — mint/persist from day one,
     even single-instance; retrofitting exactly-once later is painful.
- **Production:** Redis stream = media-engine failover buffer; Kafka = durable
  history/audit log; many stateless orchestrator instances. See
  `production-architecture.drawio.svg`.

---

## Summary

- **Media engine** (stateful, holds the connection): a crash always leaves *some*
  loss — persist the **understanding** (STT output + session state) durably, treat
  raw in-flight audio as best-effort; the L4 LB cannot hide a dead backend.
- **Orchestrator** (stateless backend): not a SPOF — re-route to a healthy
  instance; the real work is **idempotent side effects + turn checkpointing**.
- **STT / TTS** (stateless, no side effects): the benign tier — re-route +
  recompute + resume-at-position; safe to retry or even hedge.
- **Money movement: never auto-retry.** Reconcile via a read-only status check,
  tell the customer the truth, and **escalate rather than tell them to retry**.
