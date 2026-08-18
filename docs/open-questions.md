# Open questions

Things we do not know yet, recorded while building the prototype.

These are distinct from [incongruities.md](incongruities.md), which records where
the specification and the framework disagree with each other. Nothing here is a
disagreement between documents. These are questions about the method itself —
mostly empirical, a few design forks — and most of them are answerable with
measurements we can take on two hosts this week.

Scope note: the current focus is **getting solid single-point-in-time
measurements**. Questions about evaluating a time series, geofence semantics, and
longitudinal claims are real but downstream, and are parked in §6 rather than
worked through here.

Each question ends with what would settle it, because a question that cannot be
closed is a mood rather than a research item.

---

## 1. Estimation and sampling

### 1.1 How many probes does a burst need?

The estimator is `min` over a burst, because network noise is one-sided —
queueing and routing only ever add delay, so the minimum converges on true
propagation time **from above** and the mean is biased by an amount that grows
with load.

What we do not know is the *rate* of that convergence, and it is the number that
decides everything about cost.

The convergence rate is a property of the delay distribution's left tail — the
shape of the density just above the hard floor. If that tail behaves like a power
law with exponent α, the expected excess of `min(N)` over the true floor falls
roughly as `N^(-1/α)`. The practical consequence is that **returns are polynomial,
not exponential**: there is a knee, and past it each additional probe buys metres
instead of kilometres. Nobody has measured where the knee sits for a real path.

**Why it matters.** This is the entire resource-to-accuracy tradeoff. It converts
directly: probes per measurement, seconds of wall clock, CPU spent signing, and
kilometres of bound tightness are all the same currency once α is known.

**What would settle it.** Run one very large burst — 10,000 pairs — on the
Falkenstein↔Helsinki path. Then bootstrap-subsample it at N = 1, 2, 5, 10, 20,
50, 100, 500 and plot the distribution of `min(N)` against N. The curve is the
answer, and its knee is the operating point. Repeat at three times of day, since
α may itself be load-dependent.

### 1.2 Does burst spacing change what you learn?

Fifty probes fired 20 ms apart and fifty fired 2 s apart cost the same in CPU and
packets but sample the network differently. A tight burst shares a routing state,
a queue occupancy, and possibly a single scheduling quantum on both hosts — so
its samples are correlated, and the effective sample size is smaller than N. A
spread burst sees more independent network states but takes longer, which is
exactly what an interactive challenge cannot afford.

There is also a self-interference risk: a tight burst can queue behind itself,
in which case you are measuring your own traffic.

**Why it matters.** It decides whether "answer in one second" and "answer well"
are compatible. If correlation within a tight burst is severe, the interactive
product primitive is weaker than its probe count suggests and we should say so.

**What would settle it.** Fix N = 50 and sweep the inter-probe interval across
1 ms, 5 ms, 20 ms, 100 ms, 1 s. Compare the resulting `min(50)` distributions and
estimate the autocorrelation of consecutive samples at each spacing. The
effective sample size at each spacing is the deliverable.

### 1.3 How much of the residual is our own scheduler?

`-processing-delay` exists because the anchor's handling time *and the attester's
own signing time* both sit inside the interval the anchor measures. Measured
sleep-overshoot on the two candidate hosts has a floor near 22 µs, a median near
79 µs, and a p99 above 1 ms. At `c/2`, one microsecond is about 150 metres, so a
p99 scheduling excursion is worth roughly 180 km of phantom distance.

Minima suppress this, which is the whole argument for minima. But it means the
achievable floor is set by the *host*, not the network, and we have not separated
the two.

**Why it matters.** If host jitter dominates on a busy box, the cheapest accuracy
improvement is process pinning or a quieter host, not more probes. If it does
not, we should stop worrying about co-tenancy.

**What would settle it.** A loopback burst: run an attester against an anchor on
the same host. Propagation is ~0, so whatever RTT it reports *is* the combined
processing and scheduling cost. Run it under an idle host and under synthetic
load, and difference the two against the cross-host measurement.

### 1.4 Does the floor drift, and on what timescale?

The minimum over a window is only meaningful if the true floor is stable across
that window. Routing changes, diurnal congestion patterns, and maintenance
windows can all move it. If the floor moves by a millisecond, that is 150 km of
apparent movement from a machine that never moved.

**Why it matters.** It sets the maximum useful window length, and it is the
mechanism behind the much more serious problem in §4.1.

**What would settle it.** Run a low-rate continuous burst — say 20 pairs every 15
minutes — for a week between the two hosts, and plot the per-window minimum.
Stability, diurnal structure, or step changes will be visible directly.

---

## 2. Signatures

### 2.1 What does each signature scheme cost in metres?

Signing time is inside the measured interval, so signature cost converts into
apparent distance at 150 m/µs. This makes the choice of scheme an *accuracy*
decision, not just a performance one, which is not how signature schemes are
usually chosen.

Current wire format, from `internal/signed/packet.go`:

| Constant | Value |
|---|---|
| `ProbePacketSize` | 108 bytes (44 payload + 64 signature) |
| `MinReplyPacketSize` | 277 bytes |
| `MaxReplyPacketSize` | 1122 bytes |
| `signatureSize` | 64, hardcoded |

Note that `Signature` is a `[64]byte` in the struct rather than a length-prefixed
field, so changing scheme is a **wire-format change**, not a configuration one.
Any experiment here needs a format revision with an algorithm identifier.

**What would settle it.** Benchmark sign and verify latency for Ed25519 as the
baseline, then measure the delta in reported RTT when the scheme changes. Report
in metres, not microseconds — the unit conversion is the point, and it is what
makes the tradeoff legible to anyone choosing a scheme.

### 2.2 Post-quantum signatures may not fit in a datagram

This is the sharpest constraint we have found and it deserves testing early,
because it may rule out most of the PQC menu before performance is even
discussed.

Everything currently fits comfortably inside a 1500-byte MTU. Substituting a PQC
signature at NIST Level 1:

| Scheme | Signature | Probe (44 + sig) | Reply, 1 offset (387 + sig) |
|---|---|---|---|
| Ed25519 | 64 B | 108 B | 451 B |
| Falcon-512 | ~666 B | ~710 B | ~1053 B |
| ML-DSA-44 | ~2420 B | ~2464 B | ~2807 B |
| SLH-DSA-128s | ~7856 B | ~7900 B | ~8243 B |

**Fragmentation is not a mild cost here, it is disqualifying.** A fragmented probe
means the anchor's receive timestamp lands on reassembly rather than arrival, so
you are timing the kernel instead of the wire. Losing any single fragment loses
the whole measurement. And fragment reassembly delay is itself load-dependent,
which injects exactly the kind of one-sided noise the estimator is trying to
escape.

On size alone, **Falcon-512 appears to be the only NIST PQC signature that keeps
both packets inside a single datagram.** That would be a clean result, except:

**Falcon signing uses rejection sampling, so its signing time is variable and
data-dependent.** Calibration via `-processing-delay` subtracts a *constant*. It
cannot remove variance. So Falcon may trade a fixed offset — which we can
calibrate away — for jitter, which we cannot. That would be the wrong trade for
this application even though the packet fits.

**Hypothesis to test:** Falcon-512 wins on size and loses on timing variance;
ML-DSA-44 wins on timing stability and loses on size. If both hold, there may be
no good PQC option for the hot path, and the answer is §3.3 instead.

**What would settle it.** Measure the *distribution* of signing latency for each
candidate, not the mean — specifically the spread, since that is what survives
calibration. Then measure end-to-end RTT with an artificially padded packet at
each size to isolate the fragmentation penalty from the signing penalty.

### 2.3 Does this application actually need PQC on the hot path?

Worth asking before solving §2.2. "Harvest now, decrypt later" does not transfer
cleanly to signatures: forging a receipt in 2035 only helps an attacker if
somebody re-evaluates 2026 evidence and cares about the answer.

But [deployment.md](deployment.md) argues explicitly for storing raw receipts
precisely so a measurement can be re-evaluated under a better model years later.
That is the exact scenario where retroactive forgery matters. So the requirement
is real, but it is a requirement on **archived evidence**, not on the live
exchange — and those can use different mechanisms. See §3.3.

**What would settle it.** A threat-model decision rather than a measurement. Write
down who re-evaluates old receipts, when, and what they would do differently if a
receipt turned out forged.

---

## 3. Ordering and completeness

### 3.1 Nothing currently prevents an attester from cherry-picking

This is the gap that most deserves attention, and it is not addressed anywhere in
the current design.

An attester can run a thousand bursts, keep the fifty whose results it likes,
discard the rest, and present them. Every receipt it presents is
signature-valid, correctly challenged, and genuinely produced by the anchor. The
verifier cannot tell it is seeing a filtered sample.

The direction of the attack is worth being precise about, because it is not the
obvious one. Selecting for *small* measurements does not break the bound —
minima are one-sided, so no amount of selection gets you below the true floor,
and cherry-picking low merely converges on the truth faster. **The attack is
selecting for large ones, and omitting inconvenient ones entirely.** That lets a
machine appear further from an anchor than it is, which is exactly the lie you
want when the question is "are you outside this jurisdiction", and it lets a
machine silently drop the window during which it was somewhere else.

So the property we lack is not authenticity. It is **completeness**.

### 3.2 Would a hash chain give us ordering, and what should it cover?

The proposal: the attester signs the anchor's challenge nonce **plus the hash of
its previous measurement**. Each measurement then commits to its predecessor, so
the sequence becomes tamper-evident — you cannot drop, reorder, or retroactively
insert a measurement without breaking the chain.

This is a good instinct and it directly targets §3.1. Several things are
genuinely open about it.

**A self-built chain can simply be restarted.** If the attester constructs the
chain alone, it can abandon a chain it dislikes and start a fresh one, or run two
chains concurrently and present the more flattering. A chain proves internal
consistency, not that it is the *only* chain. Fixing this needs an external
witness: the anchor could sign an acknowledgement of the chain head it saw, a
head could be published periodically, or enrolment could bind a key to exactly
one chain. Each has different operational weight and none is obviously right.

**One chain or one per anchor?** A single chain across all anchors gives a global
order over everything the attester did, which is what you want for completeness.
But it serialises: a measurement against Frankfurt cannot be built until the
Amsterdam one is hashed, so a stalled or unreachable anchor blocks the chain, and
**you lose the ability to burst against several anchors in parallel.** Per-anchor
chains keep independence and parallelism, but give no cross-anchor ordering — so
an attester can still hide that it talked to a fourth anchor at all. This is a
real fork and it interacts badly with the burst primitive we want in §1.2.

**What does a chain position mean inside a burst?** If a burst is fifty pairs,
does each pair extend the chain, or does the burst extend it once as a unit? Per
pair gives finer evidence but fifty serialised dependencies at 20 ms spacing —
which may be fine, or may add a hash-and-sign to the critical path fifty times.

**Ordering is not timing.** A chain gives a *sequence*, not timestamps. It proves
B came after A; it says nothing about how long after. Freshness still has to come
from somewhere else, which raises §5.2.

**What would settle it.** Not a measurement — a design pass. Write the threat
model first: enumerate what an attester gains by omitting, reordering, forking,
and restarting, then check each candidate design against that list. Prototype
only after the witness question has an answer, because the witness is the part
that makes the chain mean anything.

### 3.3 Could a chain give post-quantum durability without PQC signatures?

Speculative, and worth recording because it connects §2 and §3.

If archived receipts are chained, their long-term integrity can rest on a **hash**
rather than on the signatures inside them. Hashes degrade far more gracefully
under quantum attack than signatures do — Grover costs a square root, so a 256-bit
hash retains roughly 128 bits, while Shor breaks Ed25519 outright.

So a periodically witnessed chain head might give long-horizon tamper-evidence
over the archive while leaving fast classical signatures on the latency-critical
path, where §2.2 says PQC may not fit anyway. This does not preserve
*authenticity* of individual old receipts against a future forger — only the
integrity of the sequence as recorded. Whether that is enough depends entirely on
the §2.3 answer.

**What would settle it.** Work out what a verifier in 2035 actually needs to
conclude, then check whether sequence integrity alone supports it.

---

## 4. Network path behaviour

### 4.1 A route change and a machine that moved look identical

The most serious limitation we have identified, and it is not in
[incongruities.md](incongruities.md).

A BGP reroute can shift the floor by milliseconds, which is hundreds of
kilometres of apparent movement. From a single anchor, that is indistinguishable
from the machine physically relocating. The measurement is doing exactly what it
claims — reporting a sound upper bound on distance — and the bound genuinely did
change. What changed underneath was the network, not the machine.

**Why it matters.** Any claim of the form "this machine has not moved" is
therefore unsupported by a single anchor, and possibly by several, since a large
reroute can affect multiple paths at once. It is the reason §6.1 is hard, and it
should be understood before anything longitudinal is built on top.

**What would settle it.** Partially, geometry: a genuine relocation should move
all anchors' bounds coherently, while a reroute typically affects one path. Test
by measuring from both hosts simultaneously and looking for correlated versus
isolated step changes. Correlating against public BGP feeds would confirm the
mechanism. Whether the discrimination is reliable enough to depend on is the open
part.

### 4.2 How stable is the path-inflation ratio?
<!--no idea what this means-->

The calibrated estimate inverts a fiber model whose constants, as
[README.md](../README.md) says, are "pinned by no specification". On the one path
measured so far the ratio of provable bound to true great-circle distance was
**2.79×**, implying an effective path velocity of 0.36c against a fiber figure
usually quoted near 0.67c. Most of that gap is routing indirection rather than
medium.

Is 2.79× typical? Is it stable for a given path? Is it learnable per-path, and
does learning it constitute calibration or overfitting?

**Why it matters.** If per-path inflation is stable and learnable, the calibrated
estimate becomes far more useful. If it is not, the calibrated estimate should
probably be de-emphasised in the output, since it currently sits next to the
provable bound and invites being read as equally solid.

**What would settle it.** Measure the ratio against known-location hosts on many
distinct paths and characterise its distribution. Two hosts is not enough — this
is the strongest argument for the London and Amsterdam anchors.

### 4.3 Does routing asymmetry matter?

The measured round trip is the sum of two paths that may differ. The provable
bound stays sound regardless — it is a round trip and does not care. But the
calibrated estimate implicitly assumes symmetry, and asymmetric routing is common
between providers.

**What would settle it.** Compare forward and reverse path characteristics for the
same anchor–attester pair, and check whether the calibrated estimate's error
correlates with the degree of asymmetry.

---

## 5. Anchor operation

### 5.1 Flat rate limit or token bucket?

`-verify-interval` is a flat minimum gap, defaulting to 29 s. It protects the
anchor: every probe costs a signature verification, so an unbounded allowlisted
key is an amplification vector.

But it is directly in tension with §1.1. Good statistics want bursts; a flat
interval forbids them. A **token bucket** — a key may spend fifty probes at once
but not sustain that rate — gives both. The open part is sizing: bucket depth and
refill rate should follow from the answer to §1.1, since the bucket should be
exactly deep enough for one statistically adequate burst.

**What would settle it.** §1.1 first, then the parameters follow arithmetically.

### 5.2 The anchor's wall clock is load-bearing after all

[deployment.md](deployment.md) states that no clock synchronisation is needed,
because every measurement is an interval on one machine. That is correct **for
distance** and it is a genuinely nice property.

It is not correct for freshness. The anchor stamps `ObservedAt` from
`time.Now()`, and a verifier's freshness check consumes that stamp. An anchor with
a badly wrong clock therefore produces distance-correct, freshness-wrong receipts,
and nothing in the exchange reveals it. The `refreshLoop` keeps the reference
offset current against the same unsynchronised clock, so it does not help.

**Why it matters.** It is a small correction to a documented claim, and freshness
is the one thing [deployment.md](deployment.md) explicitly says the verifier must
actually do.

**What would settle it.** Decide whether anchors are required to run NTP, or
whether receipts should carry an explicit clock-quality qualifier so a verifier can
discount them. Also worth noting for §3.2, since a chain gives ordering without
needing a trustworthy clock at all.

### 5.3 Where does the attester run inside a cluster?

Network position is shared by a whole rack, so measuring any machine in a
facility is nearly as informative as measuring a specific one. That cuts both
ways: one attester can stand in for its neighbours, but the method **cannot
distinguish nodes within a facility at all**. Every "which GPU" question is
therefore entirely on the binding layer described in
[gpu-binding.md](gpu-binding.md), with no help from timing.

Open: one attester per node — N keys, N enrolments, N allowlist entries — or one
per cluster, which locates the cluster and leans harder on binding? What does
enrolment look like when nodes are ephemeral?

**What would settle it.** Measure whether two machines in the same rack are
distinguishable at all at our achievable precision. Expected answer is no, and
confirming it cleanly settles the architecture.

---

## 6. Deliberately deferred

Recorded so they are not mistaken for oversights. The current focus is solid
single-point-in-time measurement; these all presuppose that problem is solved.

**6.1 Evaluating a time series.** Turning a sequence of measurements into a claim
like "consistent with this region for sixty days" is an evidence *evaluation*
question, not a collection one. It needs §4.1 answered first, since route changes
otherwise contaminate any longitudinal claim.

**6.2 Geofence semantics.** Intersecting bounds with a jurisdiction boundary,
and what a probabilistic answer means to a relying party. Partly covered by
incongruities §4.

**6.3 The anchor directory.** Anchor coordinates are self-asserted and there is
no endorsement tier. Known and documented in [README.md](../README.md); it is a
trust-infrastructure problem rather than a measurement one.

**6.4 Verifier and relying party.** Neither is in this repo. The posterior
computation, the prior, and the decision rule are all downstream.
