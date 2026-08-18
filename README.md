# plugin-rtt-anchor

A **stamp generator** for round-trip-time location evidence, extracted from
DoubleZero's RFC-16 protocol with the blockchain removed.

Two binaries. An **anchor** runs at a location you know and challenges machines
to demonstrate how far away they are. An **attester** runs on the machine in
question and answers. Each exchange produces a signed observation: an anchor's
attestation of a round-trip time it measured on its own clock.

It produces evidence. It does not compute locations, and its output should not
be read as an answer to "where is this machine".

Built for compute governance: getting timing-based evidence about where a GPU
node physically is, without trusting its operator's word for it.

## Roles

Naming follows the [Sovereignty Certificate
Specification](docs/sovcert-mapping.md), whose §3.1 and §3.2 define these terms.

| Role | Runs where | Does what |
|---|---|---|
| **anchor** | a host at known coordinates | issues a random challenge, measures the round trip on its own clock, signs the result |
| **attester** | the machine being located | echoes the challenge, signs it, collects the anchor's signed receipts |
| **verifier** | elsewhere, later | validates signatures, combines receipts from several anchors, computes a posterior |
| **relying party** | elsewhere, later | applies a threshold and decides something |

Only the first two are in this repo. That division is deliberate and load-bearing
— see below.

The direction of signing matters. The measured time rides inside the **anchor's**
signature, so an attester cannot shrink its own bound by reporting a smaller
number. Run the attester with `-raw` and it emits the anchor-signed bytes
verbatim, so a verifier checks the anchor's claim rather than the attester's
summary of it.

## What a measurement means

One exchange establishes: **the holder of key K was no further than R metres
from this anchor at this time**, where R is the round trip at vacuum light speed.

Read that carefully, because it is weaker and stranger than it looks.

**It is a bound, not a position.** Latency cannot prove a machine is *near* — only
that it is not *beyond* R. One anchor yields a disc. Several with good azimuth
spread intersect into something useful.

**The disc is not evenly filled.** The circle is the *support* of a likelihood,
not its shape. Inside it, density follows where fiber actually runs and where
machines actually are. Characterising that distribution properly is open
research; treating the radius as if belief were smeared evenly across it is
wrong.

**Noise runs one way.** Queueing and routing only add delay, so measurements err
towards *further away*, never nearer. Take minima over many samples, not means.

**It locates a key, not hardware.** Nothing here binds K to a particular GPU.
Worse, since the network can only ever inflate a measurement, moving the key is
the *only* way to fake proximity — which makes hardware binding the load-bearing
problem rather than a nice-to-have. See [gpu-binding.md](docs/gpu-binding.md).

**The anchor's position is self-asserted.** Coordinates come from `-lat`/`-lng`.
The specification expects an Endorser-signed anchor directory; this repo has no
such thing, so a measurement is exactly as good as your trust in whoever runs the
anchor.

**And no region ever reaches probability 1.** The evidence is ultimately a bit
string, and nothing about a bit string intrinsically ties it to a place. Positions
outside the light-speed bound are not impossible, merely require a broken anchor,
a leaked key, or a forged signature. What you can state is conditional on a threat
model — which is why the honest verb is "is consistent with", never "proves".
[incongruities.md](docs/incongruities.md) works through the consequences.

## Quick start

Two Linux hosts. The protocol uses `SO_TIMESTAMPNS` and epoll, so a
shared-kernel container VPS (OpenVZ, LXC) may refuse to start it — use KVM or
bare metal. No elevated capabilities needed; it is an ordinary unprivileged UDP
socket.

On the anchor host, with its real coordinates:

```sh
./anchor -id anchor-lon-1 -lat 51.5074 -lng -0.1278 -listen 0.0.0.0:8924
```

It prints its public key on startup. Open UDP 8924 inbound.

On the machine being located, get its key and send it to the anchor operator:

```sh
./attester -print-key
```

Add that to the anchor's `allowlist.txt` — one hex key per line, `#` comments and
trailing labels allowed. The anchor re-reads it every 30s, so no restart:

```
3d40...c1a9   gpu-node-3
```

Then measure:

```sh
./attester -anchor anchor.example.net:8924 -anchor-key <anchor's key>
```

```
[2026-08-17T16:22:41Z] pair #1
  anchor-measured RTT:   12.418 ms
  attester-measured RTT: 12.902 ms
  challenged:            true
  anchor position:       51.5074°N, 0.1278°W
  provable bound:        within 1861.2 km
  calibrated estimate:   1027.4 km
  signatures:            reply0=valid reply1=valid anchor-key=valid
```

The two distances are operator conveniences, not results. The **provable bound**
is a hard support boundary at vacuum c, and holds whatever the medium. The
**calibrated estimate** inverts a fiber model whose constants are ordinary
engineering figures pinned by no specification, and is a guess. Neither is a
location. A verifier should recompute from the round-trip time under its own
model rather than consume either number.

`-json` emits one object per pair; add `-raw` to include the anchor-signed bytes,
which are the part that constitutes evidence.

## Documentation

- [docs/protocol.md](docs/protocol.md) — the handshake, the wire format, why the challenge is necessary
- [docs/deployment.md](docs/deployment.md) — topology, host requirements, anchor siting, operations
- [docs/sovcert-mapping.md](docs/sovcert-mapping.md) — the roles this implements, conformance against the specification, and how output maps onto the evidence framework
- [docs/incongruities.md](docs/incongruities.md) — open questions found while implementing, including where the specification and the framework disagree
- [docs/open-questions.md](docs/open-questions.md) — what we do not know yet about the method itself: sampling statistics, signature cost, ordering, and path behaviour
- [docs/gpu-binding.md](docs/gpu-binding.md) — binding a measurement to specific hardware, and why it cannot be done naively

## Rate limiting

The anchor allows one pair per attester per `-verify-interval` (default 29s) and
drops the rest. Setting `-interval` below that on the attester silently loses
measurements. Lower both for benchmarking.

## Provenance

Extracted from [DoubleZero](https://github.com/malbeclabs/doublezero) (Apache
2.0). `internal/signed/` is copied verbatim and is the security-critical core;
`internal/offset/` is reimplemented without its Solana dependencies; everything
else is new. Full detail in [NOTICE](NOTICE).

This repository does not connect to, depend on, or interoperate with the
DoubleZero network.

## Tests

```sh
go test ./...
```

`TestReflector_Linux` and `TestSender_Linux` cover the real socket path and skip
on non-Linux hosts. To run them from macOS, cross-compile and execute on a Linux
box:

```sh
GOOS=linux GOARCH=amd64 go test -c -o signed.test ./internal/signed
scp signed.test linux-host: && ssh linux-host ./signed.test
```
