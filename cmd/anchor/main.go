// Command anchor is the reference-point half of the system. It runs on a host
// whose location is known and asserted by the operator, answers signed probes
// from allowlisted attesters, and returns replies carrying its coordinates plus
// the round-trip time it measured on its own clock.
//
// "Anchor" and "attester" follow the sovereignty-certificate vocabulary, where
// an anchor issues a challenge and signs a receipt for the round-trip time IT
// measured. That signing direction is the load-bearing part: because the RTT
// rides inside an anchor-signed reply, the attester cannot shrink its own
// provable envelope by reporting a smaller number.
//
// The anchor is deliberately self-attesting about position: coordinates come
// from -lat/-lng, not from any external authority. Upstream DoubleZero anchors
// them in an onchain device record; this fork drops that tier, so a proof is
// only as good as your trust in whoever runs the anchor. Fine for an
// experiment, not fine for a compliance claim. A real deployment would publish
// a signed anchor directory instead -- see docs/sovcert-mapping.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/location-proofs/plugin-rtt-anchor/internal/keys"
	"github.com/location-proofs/plugin-rtt-anchor/internal/offset"
	"github.com/location-proofs/plugin-rtt-anchor/internal/signed"
)

var (
	listenAddr = flag.String("listen", "0.0.0.0:8924", "UDP address to answer signed probes on")
	keyPath    = flag.String("key", "anchor-key.json", "Ed25519 key file; generated if absent")
	allowPath  = flag.String("allowlist", "allowlist.txt", "file of permitted attester public keys, one hex key per line")
	anchorID   = flag.String("id", "", "human-readable anchor identifier for logs, e.g. anchor-lon-1")
	lat        = flag.Float64("lat", 0, "anchor latitude in WGS84 decimal degrees (required)")
	lng        = flag.Float64("lng", 0, "anchor longitude in WGS84 decimal degrees (required)")

	// verifyInterval is the per-attester rate limit. The reflector allows two
	// probes per window, which is exactly one pair, then drops until it resets.
	verifyInterval = flag.Duration("verify-interval", 29*time.Second, "minimum time between probe pairs from the same attester")
	replyTimeout   = flag.Duration("reply-timeout", time.Second, "reflector socket timeout")
	refreshEvery   = flag.Duration("refresh-interval", 60*time.Second, "how often to re-stamp and re-sign the reference offset")
	reloadEvery    = flag.Duration("reload-interval", 30*time.Second, "how often to re-read the allowlist")
	verbose        = flag.Bool("verbose", false, "enable debug logging")
)

func main() {
	flag.Parse()

	log := newLogger(*verbose)
	if err := run(log); err != nil {
		log.Error("anchor exited with error", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	// Guard against the easy mistake of running with the zero coordinate, which
	// is a real point in the Atlantic and would silently produce nonsense.
	if *lat == 0 && *lng == 0 {
		return errors.New("-lat and -lng are required; refusing to attest the null island coordinate")
	}
	if *lat < -90 || *lat > 90 {
		return fmt.Errorf("-lat %v out of range [-90, 90]", *lat)
	}
	if *lng < -180 || *lng > 180 {
		return fmt.Errorf("-lng %v out of range [-180, 180]", *lng)
	}

	priv, generated, err := keys.LoadOrGenerate(*keyPath)
	if err != nil {
		return err
	}
	self := keys.PublicOf(priv)
	if generated {
		log.Info("generated new anchor key", "path", *keyPath)
	}

	allowed, err := keys.LoadAllowlist(*allowPath)
	if err != nil {
		return err
	}

	reflector, err := signed.NewReflector(*listenAddr, *replyTimeout, signed.NewEd25519Signer(priv), self, allowed, *verifyInterval)
	if err != nil {
		return fmt.Errorf("create reflector: %w", err)
	}
	defer reflector.Close()
	if *verbose {
		reflector.SetLogger(log)
	}

	reflector.SetOffsets(referenceOffset(priv, self))

	// The public key is the one thing an operator must copy to each attester,
	// so log it unmissably rather than only at debug level.
	log.Info("anchor listening",
		"id", displayID(self),
		"addr", *listenAddr,
		"port", reflector.Port(),
		"public_key", keys.Format(self),
		"lat", *lat,
		"lng", *lng,
		"allowed_attesters", len(allowed),
	)
	if len(allowed) == 0 {
		log.Warn("allowlist is empty; every probe will be dropped until keys are added", "path", *allowPath)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go refreshLoop(ctx, log, reflector, priv, self)
	go reloadLoop(ctx, log, reflector)

	if err := reflector.Run(ctx); err != nil && ctx.Err() == nil {
		return fmt.Errorf("reflector: %w", err)
	}
	log.Info("anchor shut down")
	return nil
}

// displayID prefers the operator-supplied id, falling back to a short key
// prefix so logs from several anchors stay distinguishable either way.
func displayID(self [32]byte) string {
	if *anchorID != "" {
		return *anchorID
	}
	return keys.Format(self)[:12]
}

// referenceOffset builds the anchor's self-signed statement of where it is.
//
// RttNs is zero because the anchor is the root of this chain -- there is no
// upstream hop to accumulate. The reflector adds the measured interval to it
// per reply, so the attester's accumulated RTT ends up being the
// anchor-to-attester time alone, measured from these coordinates.
func referenceOffset(priv []byte, self [32]byte) [][]byte {
	o := offset.LocationOffset{
		ObservedAt:    uint64(time.Now().UnixNano()),
		Lat:           *lat,
		Lng:           *lng,
		MeasuredRttNs: 0,
		RttNs:         0,
	}
	o.Sign(priv, self)
	return [][]byte{o.Marshal()}
}

// refreshLoop re-stamps the reference offset so its timestamp stays current.
// Without this a long-running anchor would hand out an offset whose ObservedAt
// drifts arbitrarily far into the past, and any freshness check downstream
// would reject it.
func refreshLoop(ctx context.Context, log *slog.Logger, r signed.Reflector, priv []byte, self [32]byte) {
	ticker := time.NewTicker(*refreshEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.SetOffsets(referenceOffset(priv, self))
			log.Debug("refreshed reference offset")
		}
	}
}

// reloadLoop re-reads the allowlist so an attester can be enrolled without
// restarting the anchor and dropping in-flight measurements. A malformed file
// is logged and skipped, keeping the previous allowlist in force.
func reloadLoop(ctx context.Context, log *slog.Logger, r signed.Reflector) {
	ticker := time.NewTicker(*reloadEvery)
	defer ticker.Stop()

	previous := -1
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			allowed, err := keys.LoadAllowlist(*allowPath)
			if err != nil {
				log.Warn("failed to reload allowlist; keeping previous", "path", *allowPath, "error", err)
				continue
			}
			r.SetAuthorizedKeys(allowed)
			if len(allowed) != previous {
				log.Info("allowlist reloaded", "allowed_attesters", len(allowed))
				previous = len(allowed)
			}
		}
	}
}

func newLogger(debug bool) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}
