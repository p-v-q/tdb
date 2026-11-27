package vault

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// slowDown lowers the KDF cost for the duration of a test. Production always
// uses DefaultIterations (600 000); the knob exists only so this suite does
// not burn seconds per derivation, and it must be restored.
func slowDown(t *testing.T) {
	t.Helper()
	iterations = 1000
	t.Cleanup(func() { iterations = DefaultIterations })
}

// sealFor is a Create that must not fail, for tampering tests.
func sealFor(t *testing.T, passphrase, hint string, plaintext []byte) (*Envelope, *Key) {
	t.Helper()
	env, key, err := Create(passphrase, hint, plaintext)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return env, key
}

func TestCreateOpenRoundTrip(t *testing.T) {
	slowDown(t)
	plaintext := []byte(`[{"name":"shop","database":"shop"}]`)
	env, _, err := Create("a phrase", "hint", plaintext)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if env.KDF.Iterations != 1000 {
		t.Fatalf("KDF iterations = %d, want the injected 1000", env.KDF.Iterations)
	}
	if env.KDF.Algo != "pbkdf2-sha256" || env.Cipher.Algo != "aes-256-gcm" || env.Version != Version {
		t.Fatalf("envelope must be self-describing: %+v", env)
	}
	if string(env.Ciphertext) == string(plaintext) {
		t.Fatal("the payload may not appear in the clear inside the envelope")
	}
	got, _, err := Open("a phrase", env)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestOpenRejectsWrongPassphrase(t *testing.T) {
	slowDown(t)
	env, _ := sealFor(t, "a phrase", "", []byte("secret"))
	plaintext, _, err := Open("wrong phrase", env)
	if err == nil {
		t.Fatal("a wrong passphrase must not open the envelope")
	}
	if !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("err = %v, want ErrWrongPassphrase", err)
	}
	if plaintext != nil {
		t.Fatal("a failed open must not return plaintext")
	}
}

func TestOpenRejectsTampering(t *testing.T) {
	slowDown(t)
	plaintext := []byte("secret data")
	env, _ := sealFor(t, "pw", "", plaintext)

	flip := func(b []byte) []byte { copy := append([]byte(nil), b...); copy[0] ^= 1; return copy }

	// A flipped ciphertext byte.
	broken := *env
	broken.Ciphertext = flip(env.Ciphertext)
	if _, _, err := Open("pw", &broken); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("tampered ciphertext: err = %v, want ErrWrongPassphrase", err)
	}
	// A flipped nonce byte.
	broken = *env
	broken.Cipher.Nonce = flip(env.Cipher.Nonce)
	if _, _, err := Open("pw", &broken); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("tampered nonce: err = %v, want ErrWrongPassphrase", err)
	}
	// A flipped salt byte.
	broken = *env
	broken.KDF.Salt = flip(env.KDF.Salt)
	if _, _, err := Open("pw", &broken); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("tampered salt: err = %v, want ErrWrongPassphrase", err)
	}
	// An unsupported version means a clear error, not "wrong passphrase".
	broken = *env
	broken.Version = Version + 1
	if _, _, err := Open("pw", &broken); err == nil || errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("unknown version: err = %v, want an explicit unsupported-version error", err)
	}
}

func TestOpenRejectsDamagedParameters(t *testing.T) {
	slowDown(t)
	env, _ := sealFor(t, "pw", "", []byte("secret"))

	// A too-short nonce.
	broken := *env
	broken.Cipher.Nonce = env.Cipher.Nonce[:8]
	if _, _, err := Open("pw", &broken); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("short nonce: err = %v, want ErrWrongPassphrase", err)
	}
	// A too-short salt.
	broken = *env
	broken.KDF.Salt = env.KDF.Salt[:8]
	if _, _, err := Open("pw", &broken); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("short salt: err = %v, want ErrWrongPassphrase", err)
	}
	// Iterations far below the floor: a lowered envelope must not make
	// brute force cheap.
	broken = *env
	broken.KDF.Iterations = MinIterations - 1
	if _, _, err := Open("pw", &broken); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("iterations below the floor: err = %v, want ErrWrongPassphrase", err)
	}
}

// inflatedEnvelope returns a copy of env whose declared iteration count is n.
// The other parameters stay untouched (legal salt, legal algorithms), so the
// rejection can only ever be about the cost, not about malformed data.
func inflatedEnvelope(env *Envelope, n int) *Envelope {
	inflated := *env
	inflated.KDF.Iterations = n
	return &inflated
}

// TestOpenRejectsAnInflatedIterationCount pins the DoS ceiling: a tampered
// envelope that declares a billion rounds must be refused before the KDF
// runs, and as damaged data — the same error as any other way of failing to
// open an envelope, so an attacker cannot distinguish "bumped up to burn
// CPU" from "file mangled".
func TestOpenRejectsAnInflatedIterationCount(t *testing.T) {
	slowDown(t)
	env, _ := sealFor(t, "pw", "", []byte("secret"))

	started := time.Now()
	plaintext, key, err := Open("pw", inflatedEnvelope(env, 1_000_000_000))
	elapsed := time.Since(started)
	if !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("inflated iterations: err = %v, want ErrWrongPassphrase", err)
	}
	if plaintext != nil || key != nil {
		t.Fatal("a rejected envelope must not yield plaintext or a key")
	}
	if elapsed >= time.Second {
		t.Fatalf("the rejection must happen before the KDF runs, took %v", elapsed)
	}
}

// TestOpenRejectsAbsurdIterationCounts pins the ceiling on the path a real
// tamper arrives through: an envelope decoded from a settings file. A
// declared round count of 10⁹ must be refused before the KDF runs — proven
// by the rejection taking under a second instead of minutes of PBKDF2 work.
func TestOpenRejectsAbsurdIterationCounts(t *testing.T) {
	slowDown(t)
	env, _ := sealFor(t, "pw", "", []byte("secret"))

	// Through JSON, exactly like a file: marshal, tamper with the declared
	// cost, decode again. The other parameters stay legal, so the rejection
	// can only ever be about the cost.
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var tampered Envelope
	if err := json.Unmarshal(raw, &tampered); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tampered.KDF.Iterations = 1_000_000_000

	started := time.Now()
	plaintext, key, err := Open("pw", &tampered)
	elapsed := time.Since(started)
	if !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("absurd iterations: err = %v, want ErrWrongPassphrase", err)
	}
	if plaintext != nil || key != nil {
		t.Fatal("a rejected envelope must not yield plaintext or a key")
	}
	if elapsed >= time.Second {
		t.Fatalf("the rejection must happen before the KDF runs, took %v", elapsed)
	}
}

// TestOpenToleratesIterationsAtTheCeiling pins that the ceiling is inclusive
// and scaled to the KDF cost this build currently uses: with the test knob at
// 1000 the window tops out at 10 000, and an envelope declaring exactly that
// (with a key genuinely derived at that cost) still opens.
func TestOpenToleratesIterationsAtTheCeiling(t *testing.T) {
	slowDown(t) // knob 1000 → window [1000, 10 000]

	salt := bytes.Repeat([]byte("s"), saltSize)
	kdf := KDF{Algo: kdfAlgo, Iterations: maxIterations(), Salt: salt}
	key, err := deriveKey("pw", kdf)
	if err != nil {
		t.Fatalf("deriveKey at the ceiling: %v", err)
	}
	env := &Envelope{Version: Version, KDF: kdf, Cipher: Cipher{Algo: cipherAlgo}}
	if err := seal(env, key, []byte("secret")); err != nil {
		t.Fatalf("seal: %v", err)
	}
	got, _, err := Open("pw", env)
	if err != nil {
		t.Fatalf("Open at exactly the ceiling: %v", err)
	}
	if string(got) != "secret" {
		t.Fatalf("payload = %q, want %q", got, "secret")
	}

	// One round past the ceiling is damaged again.
	over := *env
	over.KDF.Iterations = maxIterations() + 1
	if _, _, err := Open("pw", &over); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("iterations past the ceiling: err = %v, want ErrWrongPassphrase", err)
	}
}

// TestIterationWindowTracksTheKnob pins how the accepted cost window scales:
// the test-only knob moves both bounds, the production default keeps the
// ceiling far above the floor, and a normal envelope is never caught in
// between.
func TestIterationWindowTracksTheKnob(t *testing.T) {
	// With the knob at its default, the window is [MinIterations,
	// 10×DefaultIterations] and the ceiling cannot collide with the floor.
	iterations = DefaultIterations
	t.Cleanup(slowDownRestore)
	if lo, hi := minIterations(), maxIterations(); lo != MinIterations || hi != 10*DefaultIterations {
		t.Fatalf("production window = [%d, %d], want [%d, %d]", lo, hi, MinIterations, 10*DefaultIterations)
	}
	if hi := maxIterations(); hi <= minIterations() {
		t.Fatalf("the ceiling must always sit above the floor: [%d, %d]", minIterations(), hi)
	}

	// The knob drags both bounds along, so the envelopes the tests create
	// (declared cost = the knob) stay inside the window.
	slowDown(t)
	if lo, hi := minIterations(), maxIterations(); lo != 1000 || hi != 10_000 {
		t.Fatalf("injected window = [%d, %d], want [1000, 10000]", lo, hi)
	}
	if env, _ := sealFor(t, "pw", "", []byte("secret")); env.KDF.Iterations != 1000 {
		t.Fatalf("declared cost = %d, want the injected 1000", env.KDF.Iterations)
	} else if _, _, err := Open("pw", env); err != nil {
		t.Fatalf("a normal injected envelope must open, err = %v", err)
	}

	// Pathological deflation: even a tiny knob keeps a usable window, never
	// an inverted one.
	iterations = 7
	t.Cleanup(slowDownRestore)
	if lo, hi := minIterations(), maxIterations(); hi < lo {
		t.Fatalf("the window inverted itself: [%d, %d]", lo, hi)
	}
}

// slowDownRestore restores the KDF cost knob without a *testing.T, for
// t.Cleanup inside tests that move the knob by hand.
func slowDownRestore() { iterations = DefaultIterations }

func TestResealKeepsSaltRotatesNonce(t *testing.T) {
	slowDown(t)
	env, key, err := Create("pw", "", []byte("first"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	salt := append([]byte(nil), env.KDF.Salt...)
	oldNonce := append([]byte(nil), env.Cipher.Nonce...)

	if err := env.Reseal(key, []byte("second")); err != nil {
		t.Fatalf("Reseal: %v", err)
	}
	if !bytes.Equal(salt, env.KDF.Salt) {
		t.Fatal("Reseal must keep the salt: the key was derived from it")
	}
	if bytes.Equal(oldNonce, env.Cipher.Nonce) {
		t.Fatal("Reseal must draw a fresh nonce; AES-GCM may not reuse one under a key")
	}

	got, gotKey, err := Open("pw", env)
	if err != nil {
		t.Fatalf("Open after reseal: %v", err)
	}
	if string(got) != "second" {
		t.Fatalf("plaintext = %q, want %q", got, "second")
	}
	if gotKey.kdf.Salt == nil || !bytes.Equal(gotKey.kdf.Salt, salt) {
		t.Fatal("the reopened key must still come from the same salt")
	}

	// The same plaintext resealed again also gets a fresh nonce: saves must
	// not reuse nonces even when the payload does not change.
	before := append([]byte(nil), env.Cipher.Nonce...)
	if err := env.Reseal(key, []byte("second")); err != nil {
		t.Fatalf("Reseal again: %v", err)
	}
	if bytes.Equal(before, env.Cipher.Nonce) {
		t.Fatal("resealing the same plaintext must still rotate the nonce")
	}
}

func TestResealRejectsForeignEnvelope(t *testing.T) {
	slowDown(t)
	env, key, err := Create("pw", "", []byte("one"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	other, _, err := Create("another", "", []byte("two"))
	if err != nil {
		t.Fatalf("Create (second): %v", err)
	}
	// A key cannot seal an envelope derived from a different salt.
	if err := other.Reseal(key, []byte("three")); err == nil {
		t.Fatal("a key must never be dropped onto a foreign envelope")
	}

	key.Zero()
	if err := env.Reseal(key, []byte("again")); err == nil {
		t.Fatal("a zeroed key must not reseal anything")
	}
	if err := env.Reseal(nil, []byte("again")); err == nil {
		t.Fatal("a missing key must not reseal anything")
	}
}

func TestChangePassphraseRetiresTheOld(t *testing.T) {
	slowDown(t)
	plaintext := []byte(`["kept"]`)
	env, _, err := Create("old pw", "old hint", plaintext)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	oldSalt := append([]byte(nil), env.KDF.Salt...)

	if _, err := env.ChangePassphrase("new pw", "new hint", plaintext); err != nil {
		t.Fatalf("ChangePassphrase: %v", err)
	}
	if bytes.Equal(oldSalt, env.KDF.Salt) {
		t.Fatal("ChangePassphrase must derive from a new salt")
	}
	if _, _, err := Open("old pw", env); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("the old passphrase must be retired, err = %v", err)
	}
	got, _, err := Open("new pw", env)
	if err != nil {
		t.Fatalf("Open with the new passphrase: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("payload = %q, want %q", got, plaintext)
	}
	if env.Hint != "new hint" {
		t.Fatalf("hint = %q, want %q", env.Hint, "new hint")
	}
}

// TestChangePassphraseWipesTheRetiredMaterial pins the hygiene of the
// envelope swap: the ciphertext, nonce and salt replaced by a passphrase
// change are key material, and must be wiped in place rather than left for
// the garbage collector to reclaim intact.
func TestChangePassphraseWipesTheRetiredMaterial(t *testing.T) {
	slowDown(t)
	plaintext := []byte(`["kept"]`)
	env, _, err := Create("old pw", "old hint", plaintext)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	retired := [][]byte{env.Ciphertext, env.Cipher.Nonce, env.KDF.Salt}
	if _, err := env.ChangePassphrase("new pw", "new hint", plaintext); err != nil {
		t.Fatalf("ChangePassphrase: %v", err)
	}
	for i, b := range retired {
		for _, c := range b {
			if c != 0 {
				t.Fatalf("retired slice %d still holds bytes: %q", i, b)
			}
		}
	}
	// The swap must not have damaged the fresh envelope.
	if _, _, err := Open("new pw", env); err != nil {
		t.Fatalf("Open with the new passphrase: %v", err)
	}
}

func TestHintIsPlaintextAndEditable(t *testing.T) {
	slowDown(t)
	env, _, err := Create("pw", "the office one", []byte("secret"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if env.Hint != "the office one" {
		t.Fatalf("hint = %q", env.Hint)
	}
	env.SetHint("still a reminder, never a password")
	if env.Hint != "still a reminder, never a password" {
		t.Fatalf("SetHint left %q", env.Hint)
	}
	// Only the data must encode to base64-looking bytes; the hint stays
	// readable text by design.
	if !strings.Contains(env.Hint, "reminder") {
		t.Fatal("the hint must not be transformed")
	}
}

func TestHintIsTruncatedToTheCap(t *testing.T) {
	slowDown(t)
	long := strings.Repeat("х", MaxHintSize+50) // multibyte runes on purpose
	env, _, err := Create("pw", long, []byte("secret"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if n := len([]rune(env.Hint)); n != MaxHintSize {
		t.Fatalf("hint = %d runes, want it capped at %d", n, MaxHintSize)
	}
	env.SetHint(long)
	if n := len([]rune(env.Hint)); n != MaxHintSize {
		t.Fatalf("SetHint left %d runes, want the cap", n)
	}
}

func TestKeyReportsAndDropsItsKDF(t *testing.T) {
	slowDown(t)
	_, key, err := Create("pw", "", []byte("secret"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if key.Iterations() != 1000 {
		t.Fatalf("Iterations() = %d, want 1000", key.Iterations())
	}
	key.Zero()
	if key.Iterations() != 0 {
		t.Fatal("a zeroed key must not report iterations")
	}
}
