# Binding a measurement to hardware

The system locates a key. For compute governance we want to locate a *GPU*.
This document explains why that gap cannot be closed the obvious way, what the
workable construction is, and what it still leaves open.

This is the open problem. Nothing in this repository solves it.

## Why this is the whole problem

One sentence explains why hardware binding is not an optional hardening step but
the load-bearing part of the design:

> **You cannot beat the bound with the network, only by moving the key.**

Every network-layer manipulation — tunnels, proxies, congestion, deliberate
queueing — *adds* delay. Added delay makes a machine appear further away, never
nearer. No amount of network trickery lets an attacker claim to be closer to an
anchor than it really is.

The only way to produce a round trip shorter than physics allows for the real
machine is for something genuinely near that anchor to hold the signing key and
answer. That is not a network attack. It is key sharing.

This decides what the geometry can and cannot do. A verifier checking that its
radii intersect will catch an attacker who has planted key-holding responders
near *several* anchors, because their claims are mutually unsatisfiable. It will
**not** catch a single relocated key: one machine near anchor A answering every
probe looks exactly like a machine near anchor A, and every receipt along the way
is valid and correctly signed.

So the measurement layer can detect distributed key sharing. Nothing in it can
detect a key that has simply moved. Closing that gap is what hardware binding is
for, and there is no substitute for it lower in the stack.

## Why you cannot sign probes with an on-die key

The instinct is to have the GPU itself sign the challenge, so the signature is
unforgeable evidence of that specific silicon. It does not work, for a reason
that is architectural rather than an implementation gap.

NVIDIA's confidential computing exposes an **attestation** interface, not a
signing oracle. You can request a report over firmware and configuration
measurements, including a caller-supplied 32-byte nonce, verified against
NVIDIA's PKI. What you cannot do is ask it to sign an arbitrary 44-byte packet
in microseconds.

Report generation involves driver round trips and takes orders of magnitude
longer than the measurement window tolerates. That latency lands *inside* the
challenge window, where it is indistinguishable from propagation time. At
roughly 300 km per millisecond of round trip, even a modest attestation delay
reads as hundreds of kilometres of phantom distance. The measurement is not
merely degraded; it is destroyed.

The constraint generalises: **anything slow cannot be inside the timing loop.**
That is the whole design tension.

## The two-loop construction

Split the work across two timescales.

**Slow loop, once per session.** Inside the attested environment, generate an
ephemeral Ed25519 keypair. Request a GPU attestation report with

```
nonce = H(ephemeral_pubkey ‖ session_id ‖ timestamp)
```

The result is a hardware-signed statement that the attested environment holds
this key. This is the same pattern as a TPM attestation key certifying an
ephemeral key.

**Fast loop, per measurement.** That ephemeral key signs the probes, exactly as
`cmd/attester` does today.

Composed, the two give: *this GPU environment was within R metres of the
anchor* — provided the key did not leave the attested environment between the
loops.

## The residual gap

That proviso is the real limit, and it does not disappear.

The attestation says the key existed inside the enclave at time T₀. The
measurement says the key was near the anchor at time T₁. Nothing binds the key
to the enclave *during* the interval. An operator who can extract the key from
the attested environment can run the fast loop from anywhere.

What narrows it:

- **Keep the key in confidential memory** and never export it, so extraction
  requires breaking the TEE rather than reading a file.
- **Re-attest frequently** — every few minutes — so the unbound interval is
  short.
- **Bracket the measurement**: attest, measure, attest again, and treat only
  measurements falling between two successful attestations as valid.
- **Bind the session**: include the anchor set and a session identifier in the
  attestation nonce, so a report cannot be reused for a different measurement
  campaign.

What does not close it: nothing available today. The GPU cannot be in the timing
loop, so there is always an interval where the binding rests on the enclave's
confidentiality rather than on a fresh hardware signature. Say so plainly in any
writeup rather than implying the composition is airtight.

## Hardware reality check

Worth stating because it determines what is testable now rather than in
principle.

Consumer Ampere hardware (RTX 3090 and similar) has **no confidential computing
support at all**. Neither does an AMD EPYC 7302, which is Rome/Zen 2 and
supports SEV and SEV-ES but not SEV-SNP — that needs Milan/Zen 3 or later. On
such a node, neither the GPU nor the CPU offers a hardware root of trust, and
the two-loop construction has nothing to anchor to. What you can still do there
is run the fast loop with a software-held key and be honest that the binding is
to a host, not to silicon.

Hopper-class parts (H100, H200, GH200) with confidential computing enabled are
what the construction needs. Confirm with `nvidia-smi conf-compute -f` before
planning around it: the capability being present in the product line does not
mean it is switched on for the card in front of you.

## A better use for a TEE, if you have one

If a confidential-computing GPU is available, signing probes is not the highest
value thing to do with it.

Running the **verifier** inside an attested TEE is stronger. Then the policy
decision — the predicate that leaves the facility — provably came from agreed
code operating on evidence that never left. That addresses containment, which
is a harder property to demonstrate than location and a more persuasive one to
an auditor.

The location measurement itself does not need a TEE at all. The challenge
response already ensures the key holder was in the timing loop; the TEE only
adds the claim about *which hardware* held the key. Those are separable
properties and worth keeping separate in any writeup, because conflating them
overstates what either mechanism delivers.
