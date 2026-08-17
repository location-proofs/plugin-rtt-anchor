# plugin-rtt-anchor

Round-trip-time location evidence, extracted from DoubleZero's RFC-16 protocol
with the blockchain removed.

Two binaries. An **anchor** runs somewhere you know the location of and
challenges machines to prove how far away they are. An **attester** runs on the
machine in question and answers. The result is a signed statement bounding how
far the attester can possibly be from the anchor.

Built to answer a narrow question for compute governance: can we get a
timing-based bound on where a GPU node physically is, without trusting its
operator's word for it?

## The three roles

Vocabulary follows the [sovereignty certificate
spec](docs/sovcert-mapping.md), so this slots into that pipeline without a
translation layer.

| Role | Runs where | Does what |
|---|---|---|
| **anchor** | a host at known coordinates | issues a random challenge, measures the round trip on its own clock, signs the result |
| **attester** | the machine being located | echoes the challenge, signs it, collects the anchor's signed replies |
| **verifier** | anywhere, later | re-checks the anchor's signature and decides what the timing implies |

The direction of signing is the load-bearing part. The measured time rides
inside the **anchor's** signature, so the attester cannot shrink its own
provable envelope by reporting a smaller number. Run the attester with `-raw`
and it emits the anchor-signed bytes verbatim, so a verifier can check them
rather than trust the summary the attester prints.

There is no verifier binary in this repo. That role belongs to the evaluation
service downstream.

## What it proves, and what it does not

A measurement says: **the holder of key K was within R metres of (lat, lon) at
time T**.

Four limits, all of them load-bearing:

**It bounds distance from above, never below.** Latency proves a machine is not
*further* than R. It cannot prove it is not *nearer*, because anyone can add
delay. One anchor gives you a disc, not a point. Three in different cities
intersect to something useful; one answers a geofence question only when its
whole disc falls inside the boundary.

**It locates a key, not hardware.** Nothing here binds K to a particular GPU.
See [docs/gpu-binding.md](docs/gpu-binding.md) — that is the open problem, and
the reason this exists.

**The anchor's position is self-asserted.** Coordinates come from `-lat`/`-lon`.
Upstream DoubleZero anchors them in an onchain device record attested by
independent operators; that tier is deliberately cut. A proof is exactly as good
as your trust in whoever runs the anchor.

**Unchallenged mode proves much less.** With `-unchallenged` both probes are
pre-signed and fired back to back, so nothing stops a relay near the anchor from
answering on the real machine's behalf.

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

Add that to the anchor's `allowlist.txt` — one hex key per line, `#` comments
and trailing labels allowed. The anchor re-reads it every 30s, so no restart:

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

Two distances, and the difference matters. The **provable bound** assumes vacuum
c and holds whatever the medium — it is the only number to make a claim on. The
**calibrated estimate** inverts a model of real fiber and is a best guess. A gap
between them that closes unexpectedly is the signature of an unusually direct
path, which is a thing a verifier should be suspicious of rather than pleased
about.

`-json` emits one object per pair; add `-raw` to include the anchor-signed bytes.

## Documentation

- [docs/protocol.md](docs/protocol.md) — the handshake, the wire format, why the challenge works
- [docs/deployment.md](docs/deployment.md) — topology, host requirements, anchor siting, operations
- [docs/sovcert-mapping.md](docs/sovcert-mapping.md) — how this maps onto the sovereignty certificate model, and what is not yet aligned
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
