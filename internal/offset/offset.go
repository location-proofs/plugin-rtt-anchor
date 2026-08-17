// Package offset implements the LocationOffset wire format: a signed statement
// that some reference point at (Lat, Lng) observed a round-trip time of RttNs
// to a target.
//
// The layout is byte-compatible with the upstream DoubleZero implementation
// (RFC-16), because the vendored signed package parses offset blobs using
// hardcoded byte positions -- see signed.ParseOffsetInfo. Changing any field
// width here silently breaks that parser, so the layout is asserted by
// TestWireLayout rather than left to trust.
//
// Wire layout, little-endian, 174 bytes for a reference-free offset:
//
//	  0:64   Signature        [64]byte
//	 64:65   Version          uint8
//	 65:97   AuthorityPubkey  [32]byte   signer of this offset
//	 97:129  SenderPubkey     [32]byte   identity the offset speaks for
//	129:137  ObservedAt       uint64
//	137:145  Lat              float64
//	145:153  Lng              float64
//	153:161  MeasuredRttNs    uint64     this hop only
//	161:169  RttNs            uint64     accumulated from Lat/Lng
//	169:173  TargetIP         [4]byte
//	173:174  NumReferences    uint8
//	174:...  References       recursive, 174 bytes each
//
// Upstream calls the field at 129:137 "MeasurementSlot" and fills it with a
// Solana slot number. This fork has no ledger, so it carries Unix nanoseconds
// instead. The wire position and width are unchanged; only the meaning differs.
package offset

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
)

const (
	// Version is the wire format version. Upstream RFC-16 is at version 1 and
	// this fork does not change the layout, so it stays 1.
	Version = 1

	// Size is the encoded length of a reference-free offset. It must equal
	// signed.LocationOffsetSize.
	Size = 174

	// MaxReferenceDepth bounds recursion when decoding a chain, so a hostile
	// blob cannot exhaust the stack.
	MaxReferenceDepth = 2

	// MaxTotalReferences bounds the total node count in a decoded chain.
	MaxTotalReferences = 5

	// signatureSize is the leading Ed25519 signature.
	signatureSize = 64
)

var (
	// ErrShortBuffer is returned when a blob is too small to hold an offset.
	ErrShortBuffer = errors.New("offset: buffer shorter than one offset")

	// ErrBadSignature is returned when signature verification fails.
	ErrBadSignature = errors.New("offset: signature verification failed")
)

// LocationOffset is a signed latency statement relative to a reference point.
type LocationOffset struct {
	Signature       [64]byte
	AuthorityPubkey [32]byte
	SenderPubkey    [32]byte

	// ObservedAt is Unix nanoseconds at the moment of measurement. It occupies
	// the wire slot upstream calls MeasurementSlot. It is the only replay
	// defense in this fork -- verifiers must reject stale offsets themselves.
	ObservedAt uint64

	Lat float64
	Lng float64

	// MeasuredRttNs is the RTT of this hop alone.
	MeasuredRttNs uint64

	// RttNs is the accumulated RTT from (Lat, Lng), i.e. this hop plus every
	// hop in References. For a root offset it equals MeasuredRttNs.
	RttNs uint64

	TargetIP [4]byte

	// References are the offsets this one builds on. Empty for a root offset,
	// which is what a self-attesting probe emits.
	References []LocationOffset
}

// ParseIP converts a dotted-decimal IPv4 string into the TargetIP form. A
// non-IPv4 input yields the zero value rather than an error, matching upstream:
// TargetIP is descriptive metadata, not a field anything dispatches on.
func ParseIP(host string) [4]byte {
	ip := net.ParseIP(host)
	if ip == nil {
		return [4]byte{}
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return [4]byte{}
	}
	return [4]byte{ip4[0], ip4[1], ip4[2], ip4[3]}
}

// FormatIP renders a TargetIP as dotted decimal.
func FormatIP(ip [4]byte) string {
	return fmt.Sprintf("%d.%d.%d.%d", ip[0], ip[1], ip[2], ip[3])
}

// EncodedLen returns the encoded size of o including its reference chain.
func (o *LocationOffset) EncodedLen() int {
	n := Size
	for i := range o.References {
		n += o.References[i].EncodedLen()
	}
	return n
}

// Marshal encodes the offset and its reference chain.
func (o *LocationOffset) Marshal() []byte {
	buf := make([]byte, 0, o.EncodedLen())
	buf = append(buf, o.Signature[:]...)
	return o.appendSigned(buf)
}

// SigningBytes returns the bytes covered by the signature: everything except
// the leading Signature field, including the full reference chain.
func (o *LocationOffset) SigningBytes() []byte {
	return o.appendSigned(make([]byte, 0, o.EncodedLen()-signatureSize))
}

// appendSigned writes every field except Signature, then each reference in
// full. Marshal and SigningBytes share it so the two can never drift.
func (o *LocationOffset) appendSigned(buf []byte) []byte {
	buf = append(buf, Version)
	buf = append(buf, o.AuthorityPubkey[:]...)
	buf = append(buf, o.SenderPubkey[:]...)
	buf = binary.LittleEndian.AppendUint64(buf, o.ObservedAt)
	buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(o.Lat))
	buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(o.Lng))
	buf = binary.LittleEndian.AppendUint64(buf, o.MeasuredRttNs)
	buf = binary.LittleEndian.AppendUint64(buf, o.RttNs)
	buf = append(buf, o.TargetIP[:]...)
	buf = append(buf, uint8(len(o.References)))
	for i := range o.References {
		buf = append(buf, o.References[i].Marshal()...)
	}
	return buf
}

// Unmarshal decodes an offset and its reference chain from data.
func (o *LocationOffset) Unmarshal(data []byte) error {
	n, err := o.unmarshal(data, 0)
	if err != nil {
		return err
	}
	if total := o.countReferences(); total > MaxTotalReferences {
		return fmt.Errorf("offset: %d references exceed maximum of %d", total, MaxTotalReferences)
	}
	if n != len(data) {
		return fmt.Errorf("offset: %d trailing bytes after decode", len(data)-n)
	}
	return nil
}

// unmarshal decodes one offset from the front of data and returns how many
// bytes it consumed, recursing into the reference chain.
func (o *LocationOffset) unmarshal(data []byte, depth int) (int, error) {
	if depth > MaxReferenceDepth {
		return 0, fmt.Errorf("offset: reference depth %d exceeds maximum of %d", depth, MaxReferenceDepth)
	}
	if len(data) < Size {
		return 0, ErrShortBuffer
	}

	copy(o.Signature[:], data[0:64])
	if v := data[64]; v != Version {
		return 0, fmt.Errorf("offset: unsupported version %d (expected %d)", v, Version)
	}
	copy(o.AuthorityPubkey[:], data[65:97])
	copy(o.SenderPubkey[:], data[97:129])
	o.ObservedAt = binary.LittleEndian.Uint64(data[129:137])
	o.Lat = math.Float64frombits(binary.LittleEndian.Uint64(data[137:145]))
	o.Lng = math.Float64frombits(binary.LittleEndian.Uint64(data[145:153]))
	o.MeasuredRttNs = binary.LittleEndian.Uint64(data[153:161])
	o.RttNs = binary.LittleEndian.Uint64(data[161:169])
	copy(o.TargetIP[:], data[169:173])
	numRefs := int(data[173])

	off := Size
	o.References = nil
	if numRefs > 0 {
		o.References = make([]LocationOffset, numRefs)
		for i := 0; i < numRefs; i++ {
			n, err := o.References[i].unmarshal(data[off:], depth+1)
			if err != nil {
				return 0, fmt.Errorf("offset: reference %d: %w", i, err)
			}
			off += n
		}
	}
	return off, nil
}

// countReferences counts every node in the chain below o.
func (o *LocationOffset) countReferences() int {
	n := len(o.References)
	for i := range o.References {
		n += o.References[i].countReferences()
	}
	return n
}

// Sign populates AuthorityPubkey and SenderPubkey, then signs the offset in
// place. senderPubkey is the identity the offset speaks for, which may differ
// from the signing key -- upstream separates a device from the key its agent
// signs with, and this fork keeps that split so probe keys stay rotatable.
func (o *LocationOffset) Sign(key ed25519.PrivateKey, senderPubkey [32]byte) {
	copy(o.AuthorityPubkey[:], key.Public().(ed25519.PublicKey))
	o.SenderPubkey = senderPubkey
	copy(o.Signature[:], ed25519.Sign(key, o.SigningBytes()))
}

// Verify checks this offset's own signature, ignoring its references.
func (o *LocationOffset) Verify() error {
	if !ed25519.Verify(o.AuthorityPubkey[:], o.SigningBytes(), o.Signature[:]) {
		return ErrBadSignature
	}
	return nil
}

// VerifyChain checks this offset's signature and every signature beneath it.
//
// A valid chain proves each hop was signed by the key it names. It does not
// prove those keys are trustworthy: the caller must separately decide whether
// it believes AuthorityPubkey and the (Lat, Lng) that key asserts.
func (o *LocationOffset) VerifyChain() error {
	if err := o.Verify(); err != nil {
		return err
	}
	for i := range o.References {
		if err := o.References[i].VerifyChain(); err != nil {
			return fmt.Errorf("offset: reference %d: %w", i, err)
		}
	}
	return nil
}
