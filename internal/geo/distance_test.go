package geo_test

import (
	"testing"
	"time"

	"github.com/location-proofs/plugin-rtt-anchor/internal/geo"
	"github.com/stretchr/testify/assert"
)

// Hand-computed from the sovereignty-certificate formulas, so a drift in either
// implementation shows up here rather than as two pipelines quietly disagreeing
// about where a machine is.
//
//	provable   = 0.010 / 2 * 299792458                    = 1498962.29 m
//	calibrated = 0.010 / 2 * 0.69 * 299792458 / 1.25      =  827427.18 m
func TestKnownVectors(t *testing.T) {
	rtt := 10 * time.Millisecond

	assert.InDelta(t, 1_498_962.29, geo.ProvableMaxDistance(rtt), 0.01)
	assert.InDelta(t, 827_427.18, geo.CalibratedDistance(rtt, 0), 0.01)
}

// The provable bound must always sit outside the calibrated estimate. If this
// ever inverts, a geofence check could pass on the estimate while the true
// position lies outside the region the evidence actually supports.
func TestProvableBoundExceedsCalibratedEstimate(t *testing.T) {
	for _, rtt := range []time.Duration{
		100 * time.Microsecond,
		time.Millisecond,
		10 * time.Millisecond,
		100 * time.Millisecond,
	} {
		assert.Greater(t, geo.ProvableMaxDistance(rtt), geo.CalibratedDistance(rtt, 0),
			"rtt=%s", rtt)
	}
}

func TestProcessingDelayShrinksEstimate(t *testing.T) {
	rtt := 10 * time.Millisecond

	withDelay := geo.CalibratedDistance(rtt, 500*time.Microsecond)
	withoutDelay := geo.CalibratedDistance(rtt, 0)

	assert.Less(t, withDelay, withoutDelay,
		"subtracting handling time must reduce the distance attributed to travel")
}

// Over-subtracting must not produce a negative distance, which would read as a
// position on the far side of the anchor.
func TestDelayExceedingRttYieldsZero(t *testing.T) {
	assert.Zero(t, geo.CalibratedDistance(time.Millisecond, 2*time.Millisecond))
	assert.Zero(t, geo.CalibratedDistance(time.Millisecond, time.Millisecond))
}

func TestNonPositiveRttYieldsZero(t *testing.T) {
	assert.Zero(t, geo.ProvableMaxDistance(0))
	assert.Zero(t, geo.ProvableMaxDistance(-time.Millisecond))
	assert.Zero(t, geo.CalibratedDistance(0, 0))
}

// The ~92us challenge overhead measured on a commodity VPS should read as a
// small distance, confirming the nonce echo costs precision on a scale far
// below the hundreds of kilometres a geofence decision turns on.
func TestChallengeOverheadIsSmallInDistanceTerms(t *testing.T) {
	overhead := 92 * time.Microsecond

	assert.Less(t, geo.ProvableMaxDistance(overhead), 15_000.0,
		"challenge overhead should cost well under 15 km of phantom distance")
}
