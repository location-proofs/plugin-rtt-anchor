// Package geo converts round-trip times into distances.
//
// Two conversions, and the difference between them is the point. The provable
// bound assumes vacuum c and is sound whatever the medium carries the signal.
// The calibrated estimate inverts a model of how real fiber behaves, and is
// therefore only as good as that model -- it is biased low over an unusually
// direct or fast path, which is exactly the case a verifier should be
// suspicious of.
//
// Constants match the sovereignty-certificate reference implementation
// (src/sim/physics.ts) so distances computed here are comparable with the ones
// that pipeline produces.
package geo

import "time"

const (
	// CVacuumMps is the speed of light in vacuum, m/s -- the exact SI
	// definition.
	CVacuumMps = 299_792_458.0

	// FiberVelocityFactor is signal speed in single-mode fiber as a fraction
	// of c.
	FiberVelocityFactor = 0.69

	// RouteFactor accounts for terrestrial fiber running longer than the
	// great-circle distance between two points.
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
