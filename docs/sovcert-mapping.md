# Where this fits

Two documents govern how this code should be understood, and they are not the
same document:

- **The Sovereignty Certificate Specification** (v0.1.0, Sovereignty
  Certificates Working Group) — a normative standard. It defines the roles this
  code implements and states requirements in SHALL/SHOULD language.
- **The location verification framework** — a conceptual model for what location
  evidence *is* and how belief about location should be represented.

This repo implements two roles from the specification. The framework is what
tells you how to interpret the output. Where they disagree, and they do in
places, see [incongruities.md](incongruities.md).

## What this repo is, in one line

A **stamp generator**. It produces signed observations. It does not compute
locations, and it should not be read as doing so.

## Roles, per the specification

The specification builds on RFC 9334 (RATS) and defines five roles. This repo
implements the first two and nothing else.

| Role | Spec | Here |
|---|---|---|
| **Anchor** | §3.1 — "computing entity at a known, fixed geographic location that participates in location measurement protocols by returning signed, timestamped receipts in response to probes from an attester" | `cmd/anchor` |
| **Attester** | §3.2, RFC 9334 — the entity whose evidence must be appraised | `cmd/attester` |
| **Verifier** | §3.10 — receives evidence, appraises it, computes location, issues a VAR | not implemented; downstream |
| **Endorser** | §3.3 — root of trust for reference data; publishes the signed Anchor Directory | not implemented; see gaps |
| **Relying Party** | §4.1.5 — consumes the certificate, makes authorization decisions | out of scope |

The naming in this codebase is the specification's own, not a local invention.

## What the specification asks of these two roles

**Anchor (§7.1).** Deployed in a physically secure facility with a stable, known
network location (§7.1.1). Synchronised to a reliable time source (§7.1.2) —
though note the spec's own caveat that RTT relies on local clock *stability*
rather than absolute UTC. On receiving a valid probe, generate a receipt
containing a high-precision timestamp, the challenge value, and a unique anchor
identifier, signed with the anchor's private key (§7.1.3). Optionally
participate in peer monitoring so a drifting or lying anchor is detected
(§7.1.4).

**Attester's Location Measurement Agent (§5.5).** Probe a set of trusted anchors
using a protocol capable of precise RTT measurement — ICMP, UDP and QUIC are
named (§5.5.1.1). Choose anchors to minimise geometric dilution of precision,
prioritising proximity and azimuth spread (§5.5.1.2). Collect the signed receipt
for each probe (§5.5.2.1) and validate its signature before including it
(§5.5.2.2).

That last requirement is why `attester` verifies every reply rather than
trusting the transport, and §5.5.1.2 is why [deployment.md](deployment.md)
treats anchor siting as a geometry problem rather than a procurement one.

## Conformance

| Requirement | Status |
|---|---|
| §5.5.1.1 — probe anchors over an RTT-capable protocol | Met. UDP. |
| §5.5.2.1 — collect a signed receipt per probe | Met. |
| §5.5.2.2 — validate receipt signatures before inclusion | Met. |
| §7.1.3(c) — receipt carries a unique anchor identifier | Met. The anchor's public key. |
| §7.1.3(b) — receipt carries the challenge from the attester's probe | **Not met.** The nonce runs the other way — see [incongruities #1](incongruities.md). |
| §7.1.3(a) — receipt carries a high-precision timestamp of probe receipt | **Partial.** Carries a high-precision *interval* plus a coarser absolute time. |
| §7.1.2 — anchor time synchronisation | Not enforced. No sync is needed for the interval; it matters for freshness and cross-anchor correlation. |
| §7.1.4 — anchor peer monitoring | Not implemented. |
| §5.3 — ephemeral key bound to a hardware root of trust | Not implemented. See [gpu-binding.md](gpu-binding.md). |
| §5.6 — assemble receipts into a signed Evidence Envelope | Not implemented. Output is per-probe JSON. |
| Annex A — EAT profile | Not implemented. |
| §7.2 — Endorser-signed Anchor Directory | Not implemented. Anchor keys and coordinates come from flags. |
| §6.x — everything in the Verifier role | Deliberately absent. Not this component's job. |

The gaps divide cleanly. Most are "not built yet" — envelope assembly, the EAT
profile, the directory. One is a genuine design disagreement about the nonce.
The Verifier-side absences are correct rather than missing: §6.3 places
multilateration, radius and confidence computation in the Verifier, and this
repo should not be doing any of it.

## How the output maps onto the framework

The framework's chain runs observables → stamps → evidence → assessment →
decision. This repo covers the first two links.

**Observable `O_i`** — one round-trip time, measured on the anchor's own clock
between transmitting its reply and receiving the attester's follow-up probe. A
duration, nothing more.

**Stamp `s_i = g_i(O_i)`** — the observation combined with the anchor's known
coordinates and a propagation model, yielding a *likelihood over positions*: a
hard support boundary at `c·rtt/2`, and inside it a non-uniform density that
follows network topology. One stamp is one anchor's constraint. It is not a
location and cannot become one on its own.

**Evidence `E = {s_1 … s_m}`** — stamps from several anchors, ideally with good
azimuth spread. Combining them is multilateration, which is the evidence
function's job by definition, not the stamp generator's.

**Assessment `A = (π, Q)`** — a spatiotemporal posterior and a vector of
qualifiers. π depends on the evidence, the prior and the threat model — **not**
on any claimed region. Nothing in this repo produces π.

**Credibility `Pr[C|E,θ] = π(L × T)`** — the probability mass of π falling
inside a region and time window. A *query against a stored posterior*, not a
parameter of the assessment. Asking about a different region is another query,
not a recomputation.

**Decision** — a threshold applied to that probability alongside the qualifiers,
by whoever is deciding.

### Why the region stays out of the assessment

It is tempting to hand the evidence function the geofence and let it answer
directly. Resist it. Evidence accumulates continuously and gets challenged
occasionally, and the challenges are not knowable in advance: this month a
jurisdiction, next month a specific facility, later a question nobody has
thought of yet. Binding the assessment to a region means recomputing it per
question and storing an artifact that answers only one.

Keeping π free of the region gives you one posterior and unlimited cheap
queries against it, and it puts a real architectural boundary between measuring
where something is and comparing that against a reference geometry.

### Where the platform quote goes

The specification has the attester bundle a hardware quote alongside the
location receipts (§5.4, §5.6). That quote is not a location stamp — it says
nothing about where anything is. It belongs in **Q**: it raises the cost of
forging the stamps by binding the signing key to measured hardware. It is trust
in the evidence, not evidence about position.

## The unit of storage

Store the **receipts**, not the posterior.

Receipts are signed, compact, and independently checkable years later. A
posterior is derived — it depends on the propagation model, the prior and the
threat model, all of which will improve. Storing π freezes today's modelling
into an artifact you cannot revisit; storing receipts lets you recompute last
month's measurements under next month's better model.

This also lands the containment property naturally. Receipts accumulate inside
the facility. A challenge arrives — "was this machine inside F during window
T?" — the posterior is computed locally, the query runs against it, and the
answer is the only thing that crosses the boundary.

One honest caveat: each anchor unavoidably learns that a particular key probed
it and roughly how far away it was. Keeping the evidence store in-facility
controls where the composite picture lives, not what any individual anchor
observes.
