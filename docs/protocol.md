# The protocol

How a measurement is made, why the challenge is necessary, and what the bytes
look like.

A measurement produced here is an **observation**, not a location. It bounds how
far the key holder could be from one anchor. Turning several such bounds into a
belief about where something is belongs to the evidence function downstream —
see [sovcert-mapping.md](sovcert-mapping.md).

## The problem the challenge solves

A naive latency proof is trivially forged. Ask a machine "how long does a round
trip to you take?" and it answers with whatever number it likes. Measure it
yourself and you learn the round trip to *whatever answered*, which need not be
the machine you care about — a relay near the anchor answers fast and the real
machine sits anywhere.

Signing the reply does not fix this by itself. If the attester can prepare both
of its messages in advance, it can hand them to a relay near the anchor, which
fires them back to back. The anchor sees a short interval and a valid signature,
and concludes the key holder is nearby. It is not.

The fix is to make the second message *impossible to produce in advance*.

## The exchange

```
attester                                    anchor (known lat/lon)
    |                                          |
    |  probe 0   signed over (seq, time, pk)   |
    | ---------------------------------------> |
    |                                          |  generate random 8-byte nonce
    |                                          |  store it against attester pk
    |  reply 0   nonce carried in the          |
    |            SinceLastRxNs field           |  ← start timing
    | <--------------------------------------- |
    |                                          |
    |  parse reply 0, extract nonce            |
    |  write nonce into probe 1, sign          |
    |                                          |
    |  probe 1   signed over (nonce, pk)       |
    | ---------------------------------------> |  ← stop timing
    |                                          |  compare echoed nonce
    |  reply 1   carries measured interval,    |
    |            challenged flag, anchor        |
    |            coordinates, signature         |
    | <--------------------------------------- |
```

The anchor measures from its own transmission of reply 0 to its own receipt of
probe 1. Both timestamps come from one clock on one machine, so no clock
synchronisation is required anywhere in this system — a point worth internalising,
because it removes an entire class of deployment problem.

Probe 1 cannot exist until reply 0 has arrived and been parsed, so the measured
interval has the true round-trip wire time as a floor. A relay near the anchor
does not help: the nonce still has to travel to whoever holds the key and back.

If the echoed nonce matches, the anchor sets a flag in reply 1 marking the
measurement as challenged. If it does not — a legacy or lazy attester — the flag
stays clear and the measurement is reported as unchallenged. Both are returned;
the verifier decides what an unchallenged measurement is worth.

## What the challenge costs

The nonce echo forces parsing and re-signing *inside* the measured window, so
the interval is inflated by the attester's own compute time.

Measured on a commodity VPS, loopback, so wire time is negligible and what
remains is overhead:

| mode | median anchor-measured RTT |
|---|---|
| challenged | 186 µs |
| unchallenged | 94 µs |

About **92 µs**, which reads as roughly 14 km of phantom distance. Against the
hundreds of kilometres a geofence decision turns on, that is noise. The
practical conclusion is that there is no reason to run unchallenged: the
accuracy you save is not worth the proof you give up.

Note the direction of the error. Overhead makes the attester appear *further
away*, never nearer. It costs precision but cannot manufacture a false "inside
the fence" result. If you want it back, calibrate: measure your overhead and
pass it as `-processing-delay`.

## Which direction the nonce runs, and why it matters

The Sovereignty Certificate Specification §7.1.3(b) puts the nonce the other way
round: the receipt carries "the nonce or challenge value from the Attester's
probe". This implementation does not conform, deliberately, and the two designs
buy different things.

An **attester-supplied** nonce proves receipt freshness — the anchor cannot have
manufactured the receipt before the attester chose the nonce.

An **anchor-supplied** nonce, as used here, proves causal ordering — the second
probe cannot exist before the first reply was parsed, so the measured interval
has real wire time as a floor. Without it, an attester pre-signs both probes,
hands them to a relay next to the anchor, and the anchor faithfully measures and
signs the relay's distance.

A complete implementation should carry both; it costs one field. See
[incongruities #1](incongruities.md).

## The noise is one-sided, so take minima

Queueing, scheduling and routing add delay. Nothing subtracts it. The
distribution of measured RTT for a fixed true distance therefore has a hard edge
on the near side at `2d/c` and a long tail on the far side.

Two consequences. The **minimum** over many samples converges on true
propagation time from above and is the estimator to use; the mean is biased and
the bias grows with load. And an attacker can always inflate a measurement,
never deflate one — which is why the network layer cannot manufacture proximity,
and why the only route to appearing closer is moving the key. That argument is
developed in [gpu-binding.md](gpu-binding.md).

## Wire format

Two packet types, both fixed-layout and little-endian.

### Probe, 108 bytes

| bytes | field | notes |
|---|---|---|
| 0:4 | `Seq` | sequence number |
| 4:8 | `Sec` | NTP seconds; on a challenged probe 1, the upper half of the echoed nonce |
| 8:12 | `Frac` | NTP fraction; on a challenged probe 1, the lower half of the nonce |
| 12:44 | `SenderPubkey` | the attester's Ed25519 public key |
| 44:108 | `Signature` | Ed25519 over bytes 0:44 |

The nonce is smuggled through the two timestamp fields. That is what keeps the
challenge backward compatible: an attester that ignores it produces a
structurally valid probe 1 that simply never matches, and is reported
unchallenged rather than rejected.

### Reply, 277–1122 bytes

| bytes | field | notes |
|---|---|---|
| 0:108 | `Probe` | the original probe, echoed in full |
| 108:140 | `AuthorityPubkey` | the anchor's signing key |
| 140:172 | `AnchorPubkey` | the anchor's identity key |
| 172:180 | `ObservedAt` | timestamp from the reference offset |
| 180:188 | `Lat` | float64 |
| 188:196 | `Lon` | float64 |
| 196:204 | `SinceLastRxNs` | on reply 0 the challenge nonce; on reply 1 the measured interval |
| 204:212 | `RttNs` | accumulated RTT from the reference position |
| 212 | `NumOffsets` | bit 7 is the challenged flag; bits 0–6 the offset count |
| 213:… | `Offsets` | 0–5 location offsets, 174 bytes each |
| last 64 | `Signature` | Ed25519 over everything preceding |

### Location offset, 174 bytes

A signed statement of a position and a round-trip time from it. The anchor emits
one describing itself, with `RttNs` zero because it is the root of the chain;
the reflector adds the measured interval per reply.

| bytes | field |
|---|---|
| 0:64 | `Signature` |
| 64 | `Version` |
| 65:97 | `AuthorityPubkey` |
| 97:129 | `SenderPubkey` |
| 129:137 | `ObservedAt` (Unix nanoseconds) |
| 137:145 | `Lat` |
| 145:153 | `Lon` |
| 153:161 | `MeasuredRttNs` |
| 161:169 | `RttNs` |
| 169:173 | `TargetIP` |
| 173 | `NumReferences` |

The structure is recursive — offsets may reference other offsets — which is how
upstream chains a probe's measurement onto a network device's. Here the chain is
one deep.

**This layout is not free to change.** `internal/signed` parses offset blobs at
hardcoded byte positions, so an encoder that disagrees produces silently wrong
coordinates rather than an error. `TestWireLayout` in `internal/offset` asserts
our encoding against that parser field by field; it is the test to watch.

(DoubleZero's RFC-16 documents this struct as 169 bytes. The implementation
constant is 174 and the code is authoritative — the RFC text is stale.)

## Replay and freshness

The only replay defence is `ObservedAt` in the reference offset. The anchor
re-stamps and re-signs it every 60s by default. Nothing in this repo rejects a
stale measurement — a verifier must apply its own freshness window, and should,
because a captured reply remains signature-valid indefinitely.

## Rate limiting

The anchor permits two probes — one pair — per attester per `-verify-interval`,
then drops until the window resets. Per-attester state is keyed by public key,
so a flood from one key cannot starve another.

The window also bounds how quickly an attacker can search for favourable
timing samples, which matters because the *minimum* over many measurements is
the statistic a verifier should use.
