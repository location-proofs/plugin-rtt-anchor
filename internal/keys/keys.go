// Package keys handles Ed25519 identity material: loading, generating, and
// rendering keys, plus reading the probe's allowlist of permitted senders.
//
// Key files are a JSON array of 64 byte values -- the same format solana-keygen
// writes. This fork has no ledger, but keeping the format means an existing
// keypair can be dropped in unchanged, and `solana-keygen pubkey` still reads it.
//
// Public keys are rendered as lowercase hex rather than base58. Base58 would
// require a dependency to gain nothing here: there is no chain to cross-
// reference these against.
package keys

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Load reads an Ed25519 private key from a JSON key file.
func Load(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}

	var raw []byte
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse key file %s: expected a JSON array of bytes: %w", path, err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("key file %s: got %d bytes, want %d", path, len(raw), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(raw), nil
}

// Generate creates a new keypair and writes it to path with 0600 permissions.
// It refuses to overwrite an existing file: silently replacing a key would
// invalidate every allowlist entry naming the old one.
func Generate(path string) (ed25519.PrivateKey, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("key file %s already exists; delete it first if you mean to replace it", path)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat key file: %w", err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	encoded, err := json.Marshal([]byte(priv))
	if err != nil {
		return nil, fmt.Errorf("encode key: %w", err)
	}

	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create key directory: %w", err)
		}
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return nil, fmt.Errorf("write key file: %w", err)
	}
	return priv, nil
}

// LoadOrGenerate loads the key at path, creating one if the file is absent.
// The bool reports whether a new key was generated.
func LoadOrGenerate(path string) (ed25519.PrivateKey, bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		priv, err := Generate(path)
		return priv, true, err
	}
	priv, err := Load(path)
	return priv, false, err
}

// PublicOf returns the public half of a private key as a fixed array.
func PublicOf(priv ed25519.PrivateKey) [32]byte {
	var out [32]byte
	copy(out[:], priv.Public().(ed25519.PublicKey))
	return out
}

// Format renders a public key as lowercase hex.
func Format(pub [32]byte) string {
	return hex.EncodeToString(pub[:])
}

// Parse decodes a lowercase-hex public key.
func Parse(s string) ([32]byte, error) {
	var out [32]byte
	raw, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return out, fmt.Errorf("parse public key %q: %w", s, err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return out, fmt.Errorf("parse public key %q: got %d bytes, want %d", s, len(raw), ed25519.PublicKeySize)
	}
	copy(out[:], raw)
	return out, nil
}

func parseSSHEd25519Key(line []byte) ([32]byte, error) {
	var out [32]byte

	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(line)
	if err != nil {
		return out, fmt.Errorf("failed to parse SSH authorized key: %w", err)
	}

	cryptoPubKey, ok := pubKey.(ssh.CryptoPublicKey)
	if !ok {
		return out, fmt.Errorf("key does not implement ssh.CryptoPublicKey")
	}

	ed25519Key, ok := cryptoPubKey.CryptoPublicKey().(ed25519.PublicKey)
	if !ok {
		return out, fmt.Errorf("expected ed25519 key, got %T", cryptoPubKey.CryptoPublicKey())
	}

	if len(ed25519Key) != ed25519.PublicKeySize {
		return out, fmt.Errorf("invalid ed25519 key size: got %d, want %d", len(ed25519Key), ed25519.PublicKeySize)
	}

	copy(out[:], ed25519Key)
	return out, nil
}

// LoadAllowlist reads permitted sender public keys, one hex key per line.
// Blank lines and everything after a '#' are ignored.
//
// This replaces upstream's onchain target discovery. The probe answers only
// senders named here, so an empty or missing allowlist means the probe replies
// to nobody -- which is the safe failure direction.
func LoadAllowlist(path string) ([][32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open allowlist: %w", err)
	}
	defer f.Close()

	var out [][32]byte
	seen := make(map[[32]byte]struct{})

	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text := scanner.Text()
		if i := strings.IndexByte(text, '#'); i >= 0 {
			text = text[:i]
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		pub, err := keyFromLine(text)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, line, err)
		}
		if _, dup := seen[pub]; dup {
			continue
		}
		seen[pub] = struct{}{}
		out = append(out, pub)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read allowlist: %w", err)
	}
	return out, nil
}

// keyFromLine takes the first whitespace-separated field, so a line may carry a
// trailing label ("<key>  gpu-node-3") for operator sanity.
func keyFromLine(text string) ([32]byte, error) {
	if strings.HasPrefix(text, "ssh-") {
		return parseSSHEd25519Key([]byte(text))
	}
	return Parse(strings.Fields(text)[0])
}
