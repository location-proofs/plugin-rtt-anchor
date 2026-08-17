# Incongruities

Findings from implementing the specification and reading it against the
location verification framework. These are open questions, not bugs, and they
are recorded rather than smoothed over because smoothing them over is how a
half-settled design gets treated as settled.

Nothing here should be read as a criticism of either document. Both are drafts,
and a specification that survives an implementation attempt with only this many
open questions is doing well.

---

## 1. The nonce runs in opposite directions

**Specification.** §7.1.3(b): the receipt SHALL contain "the nonce or challenge
value **from the Attester's probe**." The attester supplies it; the anchor
echoes it back inside a signed receipt.

**This implementation.** The anchor generates a random nonce and returns it in
its first reply. The attester must echo that nonce inside its *second*, signed
probe. The anchor measures the interval between the two.

**Why they differ, and why both are wanted.** They defend different attacks.

An attester-supplied nonce proves *receipt freshness*: the anchor cannot have
manufactured this receipt before the attester chose the nonce, so an old receipt
cannot be replayed.

An anchor-supplied nonce proves *causal ordering*: the second probe cannot exist
before the first reply arrived and was parsed, so the measured interval has the
true round-trip wire time as a floor. Without it, an attester can pre-sign both
probes and hand them to a relay parked next to the anchor, which fires them back
to back. The anchor then measures the relay's distance and signs a perfectly
valid receipt for it. The location claim is worthless and nothing in the
cryptography reveals it.

As far as we can tell the specification's direction does not defend against that
attack at all.

**A structural gap behind it.** Measuring an interval requires two events the
anchor observed itself. A single probe gives an arrival time, not a round trip.
Yet §7.1.2's note explicitly describes RTT as depending on "the Anchor's local
clock stability (differential timing)" — which presupposes a paired exchange the
specification never defines. §7.1.3 describes a one-probe, one-receipt flow that
cannot produce an anchor-measured RTT.

**Proposed resolution.** Carry both. Probe 0 carries the attester's nonce;
reply 0 carries the anchor's; probe 1 signs over the anchor's nonce; reply 1
signs both nonces, the measured interval, and a timestamp. Fresh,
anchor-measured, and impossible to pre-sign. The cost is one extra field.

---

## 2. "Confidence" is a data-quality metric, not a probability

**Specification.** §6.3.2 requires "a geographic radius (in meters) and a
confidence score (a value between 0 and 1)" where confidence "MUST be a function
of the quality and consistency of the measurement data, including factors such
as the number of successful probes, the geometric distribution of the anchors,
and the statistical variance of the RTT measurements."

**Framework.** The assessment is `A = (π, Q)` — a spatiotemporal posterior *and*
a separate vector of qualifiers. Claim credibility is `Pr[C|E,θ] = π(L × T)`,
genuine posterior mass inside the claimed region.

**The mismatch.** Everything the specification lists as an input to confidence —
probe count, anchor geometry, RTT variance — is a statement about *evidence
quality*. That is the framework's **Q**. Meanwhile the number is named and typed
as though it were the framework's **π**. The specification collapses two axes the
framework deliberately separates, and labels the result with the name of the
wrong one.

This matters practically. Two situations produce the same "radius 45 m,
confidence 0.92": excellent measurements of a machine that is genuinely where it
claims, and excellent measurements of a machine sitting just outside the
jurisdiction boundary. High evidence quality says nothing about whether the claim
is satisfied.

**Bearing on this repo.** It is why the output should be read as an observation
rather than an answer, and why no single number here should be treated as a
confidence.

---

## 3. "Radius" implies a shape the physics does not have

The speed-of-light bound genuinely is a circle — that part is radial. But it is
the **support** of the likelihood, not its shape. Inside it the density is
governed by where fiber runs and where machines actually are, and is nothing like
uniform.

Reporting a radius names the support while implying the distribution, which
invites a reader to picture an even smear over a disc. Characterising the real
distribution is open work.

The practical consequence is that the estimator matters: network noise is
**one-sided**, since queueing and routing only ever add delay. The minimum RTT
over many samples converges on true propagation time from above; the mean is
biased and the bias grows with load. Anything consuming these measurements should
take minima, not averages.

---

## 4. The specification fuses assessment and decision

**Specification.** The Verifier computes location (§6.3), evaluates policy
(§6.4), and issues a VAR carrying policy verdicts (§6.5). Assessment and
decision live in one role.

**Framework.** The evidence function produces an assessment; a separate decision
rule turns it into a verdict.

**The cost.** A relying party with a different threshold cannot reuse an
existing assessment — it must go back for a fresh appraisal. The specification
names this tradeoff in a §6.4 aside: evaluating policy at the Verifier "produces
a simplified artifact (VAR) that abstracts the complexity of geospatial
computation from downstream Relying Parties." That is a real benefit. It is also
a choice, and worth making knowingly.

**A related question, unresolved.** If the decision is a threshold applied
*before* geospatial evaluation, uncertainty is destroyed at the boundary: an
assessment collapses to "accepted, radius 45 m" and the downstream geofence check
inherits a hard number. Propagating the posterior instead would let the geofence
answer carry its own uncertainty. Nobody has worked through what the downstream
consumer does with a probabilistic answer, and that is the open end.

---

## 5. What the geometry catches, and what it does not

§6.3.1.2 requires that the intersection of computed radii be non-empty, and
treats violations of the triangle inequality as "evidence of network path
manipulation (e.g., tunneling)."

The idea is good and worth keeping, but the stated rationale is imprecise, and
the imprecision hides which attacks actually get caught.

**Delay only ever adds.** Tunnels, proxies, queueing and congestion all make
round trips longer. Longer round trips mean larger radii, larger radii intersect
more readily, and the check never fires. **Delay-based manipulation is not what
this detects.**

The only way to produce a radius that is too *small* is for something genuinely
near that anchor to hold the key and answer. That is not a network attack — it is
key sharing.

So the check catches the case where an attacker has key-holding responders near
*several* anchors: each honestly reports a short distance to its own anchor, and
those claims are mutually unsatisfiable. It does **not** catch a single relocated
key, because one machine near anchor A answering everything looks exactly like a
machine near anchor A.

**The general form.** *You cannot beat the bound with the network, only by moving
the key.* Delay lets a machine appear further away, never nearer.

That is the sharpest available argument for hardware binding: geometry catches
distributed key sharing, and nothing at the network layer catches a single key
that has moved. Only binding the key to measured hardware does. See
[gpu-binding.md](gpu-binding.md).

---

## 6. No bounded region ever reaches probability 1

The evidence is, in the end, a bit string. Nothing about a bit string
intrinsically ties it to a place. The tie comes entirely from assumptions about
who could have produced it and under what constraints — so the posterior has
support over all of spacetime and reaches zero nowhere.

This is not a quibble. It has a definite structure worth stating, because the
support falls away in tiers rather than smoothly:

| Region | What being there would require | Rough scale |
|---|---|---|
| Inside the c-bound | Nothing unusual | Ordinary measurement uncertainty |
| Outside the c-bound | A compromised anchor, an exfiltrated key, or a forged signature | Cryptographic and operational failure rates |
| Causally disconnected — another galaxy, a different century | The evidence arising by coincidence | Roughly 2⁻²⁵⁶ |

The threat model `θ` is what decides where you truncate. `Pr[C|E,θ]` is already
conditioned on it, so the framework's notation carries this correctly — but it
means θ is doing far more work than a subscript suggests, and an assessment
quoted without its threat model is not interpretable.

Two consequences worth carrying into how results are reported.

**The prior does the taming, and the prior is not evidence.** A likelihood with
support everywhere is not normalisable on its own. What makes the posterior
well-behaved is a prior that assigns essentially no mass to a datacenter in
another galaxy — and, at the scale that actually matters, concentrates mass where
infrastructure exists. Since the prior is not derived from the measurement, an
honest system states it explicitly rather than letting it hide inside a
computation.

**Verification is conditional, exactly like the cryptography it rests on.** We do
not prove a signature was not forged; we say forgery costs roughly 2¹²⁸ work.
Location verification inherits that and adds physical assumptions on top. This is
not a weakness peculiar to location — it puts location on the same footing as
every other security property anyone already relies on. It does mean the honest
verb is "is consistent with" or "is supported to degree p", never "proves".
