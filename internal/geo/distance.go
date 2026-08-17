// Package geo converts round-trip times into distances.
//
// These are conveniences for operators reading terminal output. They are not
// the authoritative computation: under the location-verification framework a
// single measurement yields a likelihood over positions, and turning a set of
// those into a posterior belongs to the evidence function, downstream and with
// access to a prior. Nothing here should be quoted as a location.
//
// Two conversions, and the difference between them is the point.
//
// ProvableMaxDistance is a hard support boundary. Beyond it the likelihood is
// not merely small -- producing a valid signature over the anchor's nonce from
// outside that radius requires a broken anchor, a leaked key, or a forged
// signature. It is a statement about cryptographic and physical impossibility,
// not about measurement error. This is the only bound the sovereignty
// certificate specification mandates: 3.6 requires "the speed of light in the
// transmission medium (or a conservative upper bound thereof)", and vacuum c is
// the conservative upper bound.
//
// CalibratedDistance is a model, not a bound. The specification pins no
// propagation constants, so the two below come from ordinary telecom
// engineering rather than from any normative source, and they are only as good
// as the assumption that a link behaves typically. A path that is unusually
// direct reads as closer than it is, which is exactly the case a verifier
// should treat as suspicious rather than favourable.
//
// The distribution between those two figures is not uniform and not radial. It
// follows where fiber actually runs and where machines actually are, and
// characterising it is open research -- so the honest output of this package is
// the hard boundary plus a clearly-labelled model, never a point estimate.
package geo

import "time"

const (
	// CVacuumMps is the speed of light in vacuum, m/s -- the exact SI
	// definition, and the basis of the support boundary.
	CVacuumMps = 299_792_458.0

	// FiberVelocityFactor is signal speed in single-mode fiber as a fraction
	// of c. Single-mode fiber has a refractive index near 1.47, giving roughly
	// 0.68; 0.69 is a common engineering figure. Not normative anywhere.
	FiberVelocityFactor = 0.69

	// RouteFactor accounts for terrestrial fiber running longer than the
	// great-circle distance, since cable follows rights of way rather than
	// geodesics. Reported ratios vary widely by region; 1.25 is a conservative
	// commonly-cited figure and is the weakest assumption in this package.
	RouteFactor = 1.25
)

// ProvableMaxDistance returns the furthest the far end could possibly be,
// halving the round trip and converting at vacuum c.
//
// This is the only bound worth making a claim on. Nothing physical crosses the
// one-way path faster, so the far end is provably within this radius whatever
// the medium.
func ProvableMaxDistance(rtt time.Duration) float64 {
	if rtt <= 0 {
		return 0
	}
	return rtt.Seconds() / 2 * CVacuumMps
}

// CalibratedDistance estimates the actual distance by inverting the fiber
// model: subtract the known processing delay, halve, convert at fiber speed,
// then undo the routing factor.
//
// processingDelay absorbs handling time at both ends -- the anchor's own
// turnaround plus, in challenged mode, the attester's signing time. Passing
// zero is always safe: it can only overstate the distance, never understate it.
//
// Returns zero when the delay accounts for the whole measurement, rather than a
// negative distance.
func CalibratedDistance(rtt, processingDelay time.Duration) float64 {
	net := rtt - processingDelay
	if net <= 0 {
		return 0
	}
	return net.Seconds() / 2 * FiberVelocityFactor * CVacuumMps / RouteFactor
}
