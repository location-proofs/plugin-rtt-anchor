package keys_test

import (
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/location-proofs/plugin-rtt-anchor/internal/keys"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateThenLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.json")

	generated, err := keys.Generate(path)
	require.NoError(t, err)

	loaded, err := keys.Load(path)
	require.NoError(t, err)
	assert.Equal(t, generated, loaded)
	assert.Equal(t, keys.PublicOf(generated), keys.PublicOf(loaded))
}

func TestGenerateWritesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.json")
	_, err := keys.Generate(path)
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "key file must not be group or world readable")
}

// A silent overwrite would invalidate every allowlist naming the old key, so
// the failure must be loud.
func TestGenerateRefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.json")
	_, err := keys.Generate(path)
	require.NoError(t, err)

	_, err = keys.Generate(path)
	assert.ErrorContains(t, err, "already exists")
}

func TestLoadOrGenerateReportsCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.json")

	first, created, err := keys.LoadOrGenerate(path)
	require.NoError(t, err)
	assert.True(t, created)

	second, created, err := keys.LoadOrGenerate(path)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first, second, "second call must reuse the key, not mint a new one")
}

func TestLoadRejectsWrongLength(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.json")
	require.NoError(t, os.WriteFile(path, []byte("[1,2,3]"), 0o600))

	_, err := keys.Load(path)
	assert.ErrorContains(t, err, "want 64")
}

func TestLoadRejectsNonJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))

	_, err := keys.Load(path)
	assert.ErrorContains(t, err, "expected a JSON array")
}

func TestFormatParseRoundTrip(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	var arr [32]byte
	copy(arr[:], pub)

	parsed, err := keys.Parse(keys.Format(arr))
	require.NoError(t, err)
	assert.Equal(t, arr, parsed)
}

func TestParseRejectsWrongLength(t *testing.T) {
	_, err := keys.Parse(hex.EncodeToString([]byte{1, 2, 3}))
	assert.ErrorContains(t, err, "want 32")
}

func TestLoadAllowlist(t *testing.T) {
	a := keys.Format([32]byte{1})
	b := keys.Format([32]byte{2})

	content := "" +
		"# leading comment\n" +
		"\n" +
		a + "\n" +
		"   " + b + "   gpu-node-3   # trailing label and comment\n" +
		a + "  # duplicate, should collapse\n"

	path := filepath.Join(t.TempDir(), "allowlist.txt")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	got, err := keys.LoadAllowlist(path)
	require.NoError(t, err)
	assert.Equal(t, [][32]byte{{1}, {2}}, got, "comments, labels, blanks and duplicates must all be handled")
}

func TestLoadAllowlistReportsBadLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowlist.txt")
	require.NoError(t, os.WriteFile(path, []byte("\n\nnot-a-key\n"), 0o600))

	_, err := keys.LoadAllowlist(path)
	assert.ErrorContains(t, err, "line 3", "operators need the line number to fix the file")
}

func TestLoadAllowlistMissingFileErrors(t *testing.T) {
	_, err := keys.LoadAllowlist(filepath.Join(t.TempDir(), "absent.txt"))
	assert.ErrorContains(t, err, "open allowlist")
}

// An empty allowlist is legal and means "answer nobody" -- the safe direction.
func TestLoadAllowlistEmptyFileIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowlist.txt")
	require.NoError(t, os.WriteFile(path, []byte("# nobody yet\n"), 0o600))

	got, err := keys.LoadAllowlist(path)
	require.NoError(t, err)
	assert.Empty(t, got)
}
