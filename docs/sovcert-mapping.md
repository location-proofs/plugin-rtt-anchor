# Mapping onto the sovereignty certificate model

This repo produces RTT evidence. The sovereignty certificate pipeline consumes
location evidence. This document says exactly how the two line up, and — more
usefully — where they do not yet.

Reference points are the sovcert spec and its TypeScript reference
implementation (`sovcert-location-proof`), particularly `src/sim/anchors.ts`,
`src/sim/physics.ts`, and `src/lp/interface.ts`.

> **Caveat before you build on this.** The Astral `VERIFY-SPEC` is v0.1.0 and
> marked Design Phase, and the residency plan that motivates this work
> explicitly warns against treating a half-read spec as settled. The alignment
> below is deliberately limited to the parts that are stable — vocabulary, units,
> and physics. The stamp-emission layer is *not* implemented here, on purpose.
> See "What is not aligned".

## Vocabulary

The naming in this repo was changed to match sovcert rather than DoubleZero,
because the downstream consumer matters more than the upstream origin.

| sovcert | here | DoubleZero (origin) |
|---|---|---|
| anchor | `cmd/anchor` | geoprobe |
| attester | `cmd/attester` | target / target-sender |
| verifier | not implemented | client oracle |
| anchor receipt | the signed reply packet | signed reply packet |
| probe nonce | the 8-byte challenge | challenge nonce |
| attester ephemeral key | the attester's Ed25519 key | target signing key |

## The structural agreement

The sovcert simulation's `AnchorReceipt` is:

```ts
{ anchor_id, probe_nonce, attester_key_thumbprint, rtt_s, t_rx }
```

Every field has a counterpart in the reply packet this system produces:

| `AnchorReceipt` | reply packet |
|---|---|
| `anchor_id` | `AnchorPubkey` |
| `probe_nonce` | echoed inside `Probe.Sec`/`Probe.Frac` |
| `attester_key_thumbprint` | `Probe.SenderPubkey` (the key itself, not a thumbprint) |
| `rtt_s` | `SinceLastRxNs` |
| `t_rx` | `ObservedAt` |

More importantly the two agree on the design point that makes any of this worth
doing. From `src/sim/anchors.ts`:

> Because `rtt_s` rides in this anchor-signed receipt, the attester cannot
> shrink the provable envelope by self-reporting a smaller time.

That is exactly the property the challenge-response preserves here, and the
reason `attester -raw` exists: it emits the anchor-signed bytes so a verifier
checks the anchor's claim rather than the attester's summary of it.

## Physics

`internal/geo/distance.go` uses the same constants as `src/sim/physics.ts`, so
distances from the two systems are directly comparable:

| constant | value |
|---|---|
| vacuum c | 299,792,458 m/s |
| fiber velocity factor | 0.69 |
| route factor | 1.25 |

Both compute the same two quantities: a **provable maximum** at vacuum c, sound
whatever the medium, and a **calibrated estimate** that inverts the fiber model
and is only as good as that model. The gap between them is the dark-fiber
signal — an unusually direct path closes it, which is a reason for suspicion
rather than confidence.

One difference worth noting. In the sovcert simulation the per-anchor processing
delay is a published directory field, drawn from 200–800 µs. Here it is an
operator-supplied `-processing-delay` flag defaulting to zero. Zero is the safe
default because it can only overstate distance. The measured challenge overhead
on a commodity VPS is about 92 µs, which is the right order of magnitude for
that directory field and is real data rather than a drawn value.

## Units

Output field names and units follow sovcert — seconds, metres, `lat`/`lon` —
rather than the nanoseconds and `lat`/`lng` the wire format uses internally.
Nanosecond values are retained alongside, because that is the precision actually
carried and rounding to seconds early would throw away the measurement.

## What is not aligned

Three gaps, listed so nobody assumes otherwise.

**No `LocationStamp` emission.** The plugin interface expects a stamp with
`lpVersion`, `locationType`, `location`, `srs`, `temporalFootprint`, `plugin`,
`signals`, and `signatures`. This repo emits its own JSON. Producing a conformant
stamp is a wrapper, not a rewrite, but it should be written against a settled
spec version and probably belongs on the TypeScript side where the rest of the
plugin machinery lives.

**Different crypto stack.** sovcert uses ES256 with JWS/JWK and EAT claims. This
uses raw Ed25519 over fixed-layout binary packets, inherited from DoubleZero.
Bridging means either re-signing the evidence into a JWS at the plugin boundary,
or teaching the verifier to check Ed25519 over the wire format. The second
preserves the original anchor signature end to end and is therefore the stronger
option, but it costs a verifier-side implementation of this wire format.

**No anchor directory.** sovcert has an endorser-signed directory publishing each
anchor's id, public key, coordinates, and calibrated delay. Here each anchor
asserts its own coordinates via a flag and the attester is handed a key on the
command line. A directory is the natural next step and is what would let a
verifier trust an anchor it has never spoken to.

**No confidence region.** A single anchor yields a disc. Turning several discs
into a region — and a claim about whether a point lies inside a policy zone — is
`src/region/` in sovcert, and belongs there rather than here. This repo's job is
to produce sound per-anchor bounds; composing them is the verifier's.

## Suggested integration shape

The cheapest path that preserves the security property:

1. Attesters run against three or more anchors, emitting `-json -raw`.
2. Something inside the facility collects those objects into a local store. It
   needs no keys and makes no trust decisions — it is a spool.
3. The verifier reads the store, checks each anchor's Ed25519 signature over the
   raw reply, discards anything stale or unchallenged by policy, and converts
   the surviving measurements into per-anchor discs.
4. Region intersection and the policy predicate happen there, in the existing
   sovcert code.

The property worth protecting through all of this is that step 3 never trusts
the attester or the spool. Everything it needs is inside an anchor signature.
