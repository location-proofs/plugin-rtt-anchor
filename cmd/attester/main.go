// Command attester runs on the machine whose location is in question -- for our
// purposes, a GPU node -- and drives the challenge-response exchange against an
// anchor.
//
// Each round sends two signed probes. The anchor answers the first with a
// random nonce; the attester echoes that nonce into the second probe before
// signing it. The anchor then measures the interval between sending its first
// reply and receiving the second probe. Because the second probe cannot be
// produced until the first reply has been parsed, that interval is bounded
// below by the real round-trip wire time, and a proxy positioned near the
// anchor cannot fake it.
//
// The measured RTT rides inside the anchor-signed reply, not in anything this
// process asserts, so a verifier need not trust the attester's own account.
// That is why -raw exists: it emits the anchor-signed bytes verbatim, letting a
// verifier re-check the signature rather than believe the summary printed here.
//
// What this establishes: the holder of this key was within some distance of the
// anchor's coordinates at a given moment. It says nothing about what hardware
// held the key. Binding that to a specific GPU is enabled via the -use-gpu flag.
package main

/*
#cgo CFLAGS: -I.
#cgo LDFLAGS: -L. -L/usr/local/cuda/lib64 -l:libgpuprover.a -lcudart_static -lrt -lpthread -ldl -lstdc++
#include <stdint.h>
#include <stddef.h>

int init_gpu_memory(int device_id, size_t size_bytes);
int run_gpu_memory_challenge(int device_id, const uint8_t *challenge_data, size_t challenge_len, uint8_t *out_digest_32);
void free_gpu_memory(int device_id);
*/
import "C"

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/location-proofs/plugin-rtt-anchor/internal/geo"
	"github.com/location-proofs/plugin-rtt-anchor/internal/keys"
	"github.com/location-proofs/plugin-rtt-anchor/internal/offset"
	"github.com/location-proofs/plugin-rtt-anchor/internal/signed"
)

var (
	anchorAddr   = flag.String("anchor", "", "anchor address as host:port (required)")
	anchorKeyHex = flag.String("anchor-key", "", "anchor's hex public key, used to verify replies (required)")
	keyPath      = flag.String("key", "attester-key.json", "Ed25519 key file; generated if absent")
	interval     = flag.Duration("interval", 30*time.Second, "delay between probe pairs")
	count        = flag.Uint("count", 0, "number of probe pairs to send; 0 runs until interrupted")
	timeout      = flag.Duration("timeout", 2*time.Second, "per-pair timeout")
	unchallenged = flag.Bool("unchallenged", false, "skip the nonce echo; faster but forgeable, and only appropriate inside a TEE")

	useGPU    = flag.Bool("use-gpu", false, "enable GPU memory-hard VRAM traversal hardware binding")
	gpuDevice = flag.Int("gpu-device", 0, "CUDA GPU device index")
	vramGB    = flag.Uint64("vram-gb", 2, "Gigabytes of VRAM to allocate for memory-hard challenge")

	// processingDelay is the calibration constant subtracted before converting
	// time to distance. It absorbs anchor-side handling plus this host's own
	// signing time, both of which inflate the measurement. Leaving it at zero
	// is safe: it only ever makes the estimate more conservative.
	processingDelay = flag.Duration("processing-delay", 0, "calibrated per-anchor processing delay to subtract before estimating distance")

	jsonOut  = flag.Bool("json", false, "emit one JSON object per pair instead of text")
	raw      = flag.Bool("raw", false, "include base64 anchor-signed reply bytes in JSON output, for independent verification")
	printKey = flag.Bool("print-key", false, "print this attester's public key and exit")
	verbose  = flag.Bool("verbose", false, "enable debug logging")
)

// GPUMemorySigner satisfies signed.Signer by executing GPU VRAM traversal
// on probe data prior to emitting the Ed25519 signature.
type GPUMemorySigner struct {
	privKey  ed25519.PrivateKey
	pubKey   ed25519.PublicKey
	deviceID int
}

func NewGPUMemorySigner(priv ed25519.PrivateKey, deviceID int, vramBytes size_t_bytes) (*GPUMemorySigner, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid Ed25519 private key length")
	}

	ret := C.init_gpu_memory(C.int(deviceID), C.size_t(vramBytes))
	if ret != 0 {
		return nil, fmt.Errorf("failed to allocate %d bytes in GPU %d VRAM", vramBytes, deviceID)
	}

	pub := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(pub, priv[32:64])

	return &GPUMemorySigner{
		privKey:  priv,
		pubKey:   pub,
		deviceID: deviceID,
	}, nil
}

type size_t_bytes uint64

func (g *GPUMemorySigner) Sign(msg []byte) []byte {
	var digest [32]byte
	var cMsg unsafe.Pointer
	if len(msg) > 0 {
		cMsg = unsafe.Pointer(&msg[0])
	}

	// 1. Force hardware VRAM memory-bandwidth pass
	ret := C.run_gpu_memory_challenge(
		C.int(g.deviceID),
		(*C.uint8_t)(cMsg),
		C.size_t(len(msg)),
		(*C.uint8_t)(unsafe.Pointer(&digest[0])),
	)
	if ret != 0 {
		panic("GPU VRAM challenge execution failed")
	}

	// 2. Sign via standard Go crypto/ed25519
	return ed25519.Sign(g.privKey, msg)
}

func (g *GPUMemorySigner) Public() ed25519.PublicKey {
	return g.pubKey
}

func main() {
	flag.Parse()

	log := newLogger(*verbose)
	if err := run(log); err != nil {
		log.Error("attester exited with error", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	priv, generated, err := keys.LoadOrGenerate(*keyPath)
	if err != nil {
		return err
	}
	self := keys.PublicOf(priv)

	// Enrolling this key in the anchor's allowlist is a prerequisite for any
	// measurement, so make it retrievable without starting a run.
	if *printKey {
		fmt.Println(keys.Format(self))
		return nil
	}
	if generated {
		log.Info("generated new attester key", "path", *keyPath)
	}

	if *anchorAddr == "" {
		return errors.New("-anchor is required")
	}
	if *anchorKeyHex == "" {
		return errors.New("-anchor-key is required; without it replies cannot be attributed to a known anchor")
	}
	anchorPub, err := keys.Parse(*anchorKeyHex)
	if err != nil {
		return err
	}
	remote, err := net.ResolveUDPAddr("udp4", *anchorAddr)
	if err != nil {
		return fmt.Errorf("resolve -anchor %q: %w", *anchorAddr, err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Decide whether to use the GPU or standard CPU signer
	var signer signed.Signer
	if *useGPU {
		vramBytes := size_t_bytes(*vramGB * 1024 * 1024 * 1024)
		gpuSigner, err := NewGPUMemorySigner(priv, *gpuDevice, vramBytes)
		if err != nil {
			return fmt.Errorf("init GPU signer: %w", err)
		}
		defer C.free_gpu_memory(C.int(*gpuDevice))
		signer = gpuSigner
		log.Info("attesting with GPU hardware proof enabled",
			"vram_gb", *vramGB,
			"gpu_device", *gpuDevice,
		)
	} else {
		signer = signed.NewEd25519Signer(priv)
		log.Info("attesting with standard CPU signing (GPU disabled)")
	}

	sender, err := signed.NewSender(ctx, "", &net.UDPAddr{Port: 0}, remote, signer, anchorPub, !*unchallenged)
	if err != nil {
		return fmt.Errorf("create sender: %w", err)
	}
	defer sender.Close()
	if *verbose {
		sender.SetLogger(log)
	}

	log.Info("attesting",
		"anchor", remote.String(),
		"anchor_key", keys.Format(anchorPub),
		"public_key", keys.Format(self),
		"challenged", !*unchallenged,
		"interval", *interval,
		"use_gpu", *useGPU,
	)

	for seq := uint32(1); ; seq++ {
		if err := ctx.Err(); err != nil {
			log.Info("shutting down")
			return nil
		}

		measure(ctx, log, sender, seq, anchorPub, self)

		if *count > 0 && uint(seq) >= *count {
			return nil
		}
		select {
		case <-ctx.Done():
			log.Info("shutting down")
			return nil
		case <-time.After(*interval):
		}
	}
}

func measure(ctx context.Context, log *slog.Logger, sender signed.Sender, seq uint32, anchorPub, self [32]byte) {
	pairCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	result, err := sender.ProbePair(pairCtx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.New("timeout")
		}
		report(log, observation{Seq: seq, Timestamp: now(), Error: err.Error()})
		return
	}

	report(log, observe(seq, result, anchorPub, self))
}

// observation is one completed measurement. Field names and units follow the
// sovereignty-certificate anchor receipt (seconds, meters, lat/lon) so it maps
// onto that model without a translation step; nanosecond values are retained
// alongside because that is the precision the wire format actually carries.
type observation struct {
	Seq       uint32 `json:"seq"`
	Timestamp string `json:"timestamp"`

	AnchorKey   string `json:"anchor_key"`
	AttesterKey string `json:"attester_key"`

	// AnchorMeasuredRttNs is the interval the anchor timed between sending
	// reply 0 and receiving probe 1. It arrives inside the anchor's signature,
	// so it is the number a verifier can rely on.
	AnchorMeasuredRttNs uint64  `json:"anchor_measured_rtt_ns"`
	AnchorMeasuredRttS  float64 `json:"anchor_measured_rtt_s"`

	// AttesterMeasuredRttNs is the lower of our own two round-trip timings.
	// Self-reported, so it is a sanity check and not evidence.
	AttesterMeasuredRttNs uint64 `json:"attester_measured_rtt_ns"`

	// Challenged reports whether the anchor confirmed our nonce echo. False
	// means the measurement carries no proof this host waited for reply 0.
	Challenged bool    `json:"challenged"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`

	// ProvableMaxDistanceM is the sound upper bound at vacuum c.
	// CalibratedDistanceM inverts the fiber model and is an estimate.
	ProvableMaxDistanceM float64 `json:"provable_max_distance_m"`
	CalibratedDistanceM  float64 `json:"calibrated_distance_m"`

	ObservedAt string `json:"observed_at,omitempty"`

	Reply0Valid    bool `json:"reply0_valid"`
	Reply1Valid    bool `json:"reply1_valid"`
	AnchorKeyMatch bool `json:"anchor_key_match"`

	// Reply0Raw and Reply1Raw are the anchor-signed reply bytes, base64. Present
	// only with -raw. They are the actual evidence: everything else in this
	// struct is a reading of them that a verifier should be able to reproduce.
	Reply0Raw string       `json:"reply0_raw,omitempty"`
	Reply1Raw string       `json:"reply1_raw,omitempty"`
	Offsets   []offsetView `json:"offsets,omitempty"`
	Error     string       `json:"error,omitempty"`
}

type offsetView struct {
	Authority string  `json:"authority"`
	Sender    string  `json:"sender"`
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	RttNs     uint64  `json:"rtt_ns"`
	Valid     bool    `json:"valid"`
}

// observe turns a raw probe pair into a reported observation.
func observe(seq uint32, result signed.ProbePairResult, anchorPub, self [32]byte) observation {
	reply := result.Reply1

	obs := observation{
		Seq:                   seq,
		Timestamp:             now(),
		AnchorKey:             keys.Format(anchorPub),
		AttesterKey:           keys.Format(self),
		AnchorMeasuredRttNs:   reply.SinceLastRxNs,
		AnchorMeasuredRttS:    float64(reply.SinceLastRxNs) / 1e9,
		AttesterMeasuredRttNs: uint64(min(result.RTT0, result.RTT1).Nanoseconds()),
		Challenged:            reply.Challenged,
		Lat:                   reply.Lat,
		Lon:                   reply.Lng,
		Reply0Valid:           result.Reply0.Verify(),
		Reply1Valid:           reply.Verify(),
		// The sender verifies replies against the expected key internally; this
		// re-check makes the binding explicit in the output rather than implied.
		AnchorKeyMatch: reply.AuthorityPubkey == anchorPub,
	}

	rtt := time.Duration(reply.RttNs) * time.Nanosecond
	obs.ProvableMaxDistanceM = geo.ProvableMaxDistance(rtt)
	obs.CalibratedDistanceM = geo.CalibratedDistance(rtt, *processingDelay)

	if reply.MeasurementSlot > 0 {
		obs.ObservedAt = time.Unix(0, int64(reply.MeasurementSlot)).UTC().Format(time.RFC3339)
	}

	if *raw {
		obs.Reply0Raw = marshalReply(result.Reply0)
		obs.Reply1Raw = marshalReply(reply)
	}

	for _, blob := range reply.Offsets {
		var o offset.LocationOffset
		if err := o.Unmarshal(blob); err != nil {
			obs.Offsets = append(obs.Offsets, offsetView{Valid: false})
			continue
		}
		obs.Offsets = append(obs.Offsets, offsetView{
			Authority: keys.Format(o.AuthorityPubkey),
			Sender:    keys.Format(o.SenderPubkey),
			Lat:       o.Lat,
			Lon:       o.Lng,
			RttNs:     o.RttNs,
			Valid:     o.VerifyChain() == nil,
		})
	}
	return obs
}

// marshalReply re-encodes a reply to its wire bytes so a verifier can check the
// anchor's signature independently.
func marshalReply(r *signed.ReplyPacket) string {
	var buf [signed.MaxReplyPacketSize]byte
	n, err := r.Marshal(buf[:])
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buf[:n])
}

func report(log *slog.Logger, obs observation) {
	if *jsonOut {
		data, err := json.Marshal(obs)
		if err != nil {
			log.Error("failed to encode observation", "error", err)
			return
		}
		fmt.Println(string(data))
		return
	}
	fmt.Print(render(obs))
}

// render formats an observation for a terminal.
func render(obs observation) string {
	var b strings.Builder

	fmt.Fprintf(&b, "\n[%s] pair #%d\n", obs.Timestamp, obs.Seq)
	if obs.Error != "" {
		fmt.Fprintf(&b, "  error: %s\n\n", obs.Error)
		return b.String()
	}

	fmt.Fprintf(&b, "  anchor-measured RTT:   %s\n", ms(obs.AnchorMeasuredRttNs))
	fmt.Fprintf(&b, "  attester-measured RTT: %s\n", ms(obs.AttesterMeasuredRttNs))
	fmt.Fprintf(&b, "  challenged:            %v\n", obs.Challenged)
	if !obs.Challenged {
		fmt.Fprintf(&b, "    (unchallenged: no proof this host waited for reply 0)\n")
	}
	fmt.Fprintf(&b, "  anchor position:       %s\n", coord(obs.Lat, obs.Lon))
	fmt.Fprintf(&b, "  provable bound:        within %.1f km\n", obs.ProvableMaxDistanceM/1000)
	fmt.Fprintf(&b, "  calibrated estimate:   %.1f km\n", obs.CalibratedDistanceM/1000)
	if obs.ObservedAt != "" {
		fmt.Fprintf(&b, "  anchor observed at:    %s\n", obs.ObservedAt)
	}
	fmt.Fprintf(&b, "  signatures:            reply0=%s reply1=%s anchor-key=%s\n",
		valid(obs.Reply0Valid), valid(obs.Reply1Valid), valid(obs.AnchorKeyMatch))

	for i, o := range obs.Offsets {
		fmt.Fprintf(&b, "\n  anchor offset [%d]\n", i+1)
		fmt.Fprintf(&b, "    authority: %s\n", o.Authority)
		fmt.Fprintf(&b, "    position:  %s\n", coord(o.Lat, o.Lon))
		fmt.Fprintf(&b, "    signature: %s\n", valid(o.Valid))
	}
	b.WriteString("\n")
	return b.String()
}

func ms(ns uint64) string { return fmt.Sprintf("%.3f ms", float64(ns)/1e6) }

func coord(lat, lon float64) string {
	ns, ew := "N", "E"
	if lat < 0 {
		lat, ns = -lat, "S"
	}
	if lon < 0 {
		lon, ew = -lon, "W"
	}
	return fmt.Sprintf("%.4f°%s, %.4f°%s", lat, ns, lon, ew)
}

func valid(ok bool) string {
	if ok {
		return "valid"
	}
	return "INVALID"
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func newLogger(debug bool) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}
