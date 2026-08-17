package offset_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/location-proofs/plugin-rtt-anchor/internal/offset"
	"github.com/location-proofs/plugin-rtt-anchor/internal/signed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sample returns an offset whose every field holds a distinct value, so a
// layout mistake that swaps two fields cannot pass unnoticed.
func sample() offset.LocationOffset {
	return offset.LocationOffset{
		ObservedAt:    0x1122334455667788,
		Lat:           51.5074,
		Lng:           -0.1278,
		MeasuredRttNs: 4_000_000,
		RttNs:         9_000_000,
		TargetIP:      offset.ParseIP("203.0.113.7"),
	}
}

func mustKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return priv
}

// TestWireLayout is the load-bearing test of this package. The vendored signed
// package reads offset blobs at hardcoded byte positions, so this asserts our
// encoder agrees with that parser field by field. If it fails, replies will
// carry silently wrong coordinates rather than erroring.
func TestWireLayout(t *testing.T) {
	o := sample()
	o.Sign(mustKey(t), [32]byte{9})
	blob := o.Marshal()

	require.Len(t, blob, offset.Size)
	require.Equal(t, signed.LocationOffsetSize, offset.Size,
		"offset.Size must track signed.LocationOffsetSize")

	info, ok := signed.ParseOffsetInfo(blob)
	require.True(t, ok, "vendored parser rejected our encoding")

	assert.Equal(t, o.ObservedAt, info.MeasurementSlot, "ObservedAt occupies the MeasurementSlot slot")
	assert.InDelta(t, o.Lat, info.Lat, 1e-9)
	assert.InDelta(t, o.Lng, info.Lng, 1e-9)
	assert.Equal(t, o.RttNs, info.RttNs)
	assert.Equal(t, o.TargetIP, info.TargetIP)
}

func TestRoundTrip(t *testing.T) {
	o := sample()
	o.Sign(mustKey(t), [32]byte{3})

	var got offset.LocationOffset
	require.NoError(t, got.Unmarshal(o.Marshal()))
	assert.Equal(t, o, got)
}

func TestRoundTripWithReference(t *testing.T) {
	root := sample()
	root.Sign(mustKey(t), [32]byte{1})

	composite := sample()
	composite.RttNs = root.RttNs + 5_000_000
	composite.References = []offset.LocationOffset{root}
	composite.Sign(mustKey(t), [32]byte{2})

	blob := composite.Marshal()
	require.Len(t, blob, 2*offset.Size)

	var got offset.LocationOffset
	require.NoError(t, got.Unmarshal(blob))
	assert.Equal(t, composite, got)
	require.Len(t, got.References, 1)
	assert.Equal(t, root, got.References[0])
}

func TestVerifyDetectsTampering(t *testing.T) {
	o := sample()
	o.Sign(mustKey(t), [32]byte{4})
	require.NoError(t, o.Verify())

	// Moving the claimed reference point must invalidate the signature --
	// otherwise an operator could restate their location after the fact.
	tampered := o
	tampered.Lat = 48.8566
	assert.ErrorIs(t, tampered.Verify(), offset.ErrBadSignature)
}

func TestVerifyChainRejectsForgedReference(t *testing.T) {
	root := sample()
	root.Sign(mustKey(t), [32]byte{1})

	composite := sample()
	composite.References = []offset.LocationOffset{root}
	composite.Sign(mustKey(t), [32]byte{2})
	require.NoError(t, composite.VerifyChain())

	// A reference rewritten after the composite was signed must be caught. The
	// composite's own signature covers the chain, so this fails at the top.
	composite.References[0].Lat = 0
	assert.Error(t, composite.VerifyChain())
}

func TestUnmarshalRejectsShortBuffer(t *testing.T) {
	var o offset.LocationOffset
	assert.ErrorIs(t, o.Unmarshal(make([]byte, offset.Size-1)), offset.ErrShortBuffer)
}

func TestUnmarshalRejectsWrongVersion(t *testing.T) {
	o := sample()
	o.Sign(mustKey(t), [32]byte{5})
	blob := o.Marshal()
	blob[64] = 99

	var got offset.LocationOffset
	assert.ErrorContains(t, got.Unmarshal(blob), "unsupported version")
}

func TestUnmarshalRejectsTrailingBytes(t *testing.T) {
	o := sample()
	o.Sign(mustKey(t), [32]byte{6})

	var got offset.LocationOffset
	assert.ErrorContains(t, got.Unmarshal(append(o.Marshal(), 0x00)), "trailing bytes")
}

func TestUnmarshalRejectsTruncatedReference(t *testing.T) {
	root := sample()
	root.Sign(mustKey(t), [32]byte{1})
	composite := sample()
	composite.References = []offset.LocationOffset{root}
	composite.Sign(mustKey(t), [32]byte{2})

	// Claim a reference but supply none of its bytes.
	truncated := composite.Marshal()[:offset.Size]

	var got offset.LocationOffset
	assert.ErrorIs(t, got.Unmarshal(truncated), offset.ErrShortBuffer)
}

func TestUnmarshalRejectsExcessiveDepth(t *testing.T) {
	// Nest deeper than MaxReferenceDepth allows.
	o := sample()
	o.Sign(mustKey(t), [32]byte{1})
	for i := 0; i < offset.MaxReferenceDepth+1; i++ {
		parent := sample()
		parent.References = []offset.LocationOffset{o}
		parent.Sign(mustKey(t), [32]byte{byte(i + 2)})
		o = parent
	}

	var got offset.LocationOffset
	assert.ErrorContains(t, got.Unmarshal(o.Marshal()), "reference depth")
}

func TestParseIPRoundTrip(t *testing.T) {
	assert.Equal(t, "203.0.113.7", offset.FormatIP(offset.ParseIP("203.0.113.7")))
	assert.Equal(t, [4]byte{}, offset.ParseIP("not-an-ip"))
	assert.Equal(t, [4]byte{}, offset.ParseIP("2001:db8::1"), "IPv6 has no IPv4 form")
}
