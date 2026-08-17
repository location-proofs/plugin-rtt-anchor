# Deployment

## Topology

```
        anchor (London)  ─┐
        anchor (Frankfurt)─┼──── UDP 8924 ────►  attester  (the GPU node)
        anchor (Amsterdam)─┘                         │
                                                     │ -json -raw
                                                     ▼
                                              local evidence store
                                                     │
                                                     ▼
                                          verifier / policy evaluation
```

Anchors are the only hosts that need to be reachable. The attester initiates
every exchange, so it works from behind NAT without inbound rules.

That direction matters more than it looks. A datacenter that refuses inbound
traffic — the common case, and one of the flagged risks for the lab cluster —
does not block this design, because nothing ever connects *to* the machine being
located.

## Host requirements

### Anchor

- **Linux on KVM or bare metal.** The reflector sets `SO_TIMESTAMPNS` and exits
  if it cannot. Shared-kernel VPS products (OpenVZ, LXC) may refuse it. Hetzner,
  OVH, Scaleway and Vultr are all fine.
- **Public static IPv4**, one inbound UDP port. IPv4 only — the socket is
  `AF_INET`.
- **No elevated capabilities.** An ordinary unprivileged UDP socket. Run as a
  normal user.
- **1 vCPU, 1 GB** is ample. The work is one socket and an epoll loop.
- **Low scheduling jitter matters more than CPU.** Jitter lands directly in the
  measurement. Avoid heavily oversubscribed budget hosts; consider pinning the
  process on a busy machine.
- **No clock synchronisation needed.** Every measurement is an interval on a
  single machine. NTP skew cannot affect a result.

### Attester

Linux, same `SO_TIMESTAMPNS` requirement. Outbound UDP only. macOS cannot run it
— the packet-layer tests build and run there, nothing else does.

## Siting anchors

This is a geometry decision, not a procurement one, and it is the decision that
most affects whether the output is useful.

One anchor gives a disc: "within R of London". That answers a geofence question
only if the whole disc falls inside the boundary you care about — which, for a
question like "is this machine in the EU", it usually will not.

Two anchors give two discs intersecting in a lens. Three give a bounded region.
Beyond three, returns diminish quickly unless the geometry is poor.

Pick anchors that surround the expected location rather than clustering on one
side; azimuth spread does more for the region than raw count. Site them near
major interconnects so the fiber path is close to the great-circle path, which
keeps the calibrated estimate honest. For European coverage, London, Frankfurt
and Amsterdam are the obvious three — mutually 300–500 km apart and where the
fiber already goes.

Physical security of an anchor is part of the threat model. An anchor that is
lying about its coordinates, or whose key has been copied, invalidates every
measurement it contributes.

## Running

Systemd unit for an anchor:

```ini
[Unit]
Description=RTT location anchor
After=network-online.target

[Service]
ExecStart=/usr/local/bin/anchor \
  -id anchor-lon-1 \
  -lat 51.5074 -lng -0.1278 \
  -listen 0.0.0.0:8924 \
  -key /etc/rtt-anchor/key.json \
  -allowlist /etc/rtt-anchor/allowlist.txt
User=rtt-anchor
Restart=always
RestartSec=5

NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/etc/rtt-anchor

[Install]
WantedBy=multi-user.target
```

The key file must be 0600 and owned by the service user. The binary refuses to
overwrite an existing key, so a restart cannot silently rotate identity and
invalidate every allowlist naming it.

## Enrolling an attester

1. On the attester: `attester -print-key`.
2. Send that key to each anchor operator over a channel you trust. It is a
   public key, so confidentiality does not matter, but authenticity does —
   an attacker who substitutes their own key gets measured in your place.
3. The operator appends it to `allowlist.txt` with a label.
4. Within 30s the anchor picks it up. No restart.

Removal is the same in reverse. There is no revocation beyond the allowlist, so
a compromised attester key is handled by deleting the line.

## Operational notes

**Rate limit.** One pair per attester per `-verify-interval` (default 29s). An
`-interval` below that silently loses measurements. For a production cadence,
30–60s is sensible; for benchmarking, lower both.

**Use the minimum, not the mean.** Network jitter only ever adds delay. The
minimum over a window is the closest estimate of true propagation time, and it
is what a verifier should consume. Publishing a mean understates how close a
machine might be, which is the wrong direction to be wrong in.

**Calibrate once per anchor.** Measure the challenge overhead by running a
sample with and without `-unchallenged` and differencing the medians. Feed the
result back as `-processing-delay`. Expect tens to hundreds of microseconds.
Leaving it at zero is safe but costs range accuracy.

**Watch for the gap closing.** If the calibrated estimate approaches the
provable bound, the path is unusually direct. Treat that as something to
investigate, not as a better measurement.

**Nothing here rejects stale evidence.** A captured reply stays
signature-valid forever. Freshness is the verifier's job and it must actually
do it.

## What to log

The anchor's stdout is the operational record: attester enrolments, allowlist
reloads, and — with `-verbose` — every dropped probe and the reason. Dropped
probes from unknown keys are the signal that either an enrolment did not
propagate or somebody is scanning you.

The attester's `-json -raw` output is the evidence record. Keep it. The parsed
fields are a convenience; the `reply1_raw` blob is the thing a verifier can
actually check, and discarding it means discarding the proof.
