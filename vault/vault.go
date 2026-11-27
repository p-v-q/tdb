// Package vault encrypts secrets at rest with a passphrase: PBKDF2-HMAC-SHA256
// derives the key, AES-256-GCM seals the payload, and a self-describing
// envelope records the parameters so future releases can still open old files.
//
// The envelope deliberately stores version and algorithm names: a future build
// can raise the iteration count or switch the cipher and still read what an
// older build wrote. A wrong passphrase is indistinguishable from a damaged
// file by design (GCM authentication fails for both), and the error message
// says so instead of pretending to know which.
//
// Standard library only — no new dependency is acceptable for code that must
// stay auditable forever.
package vault

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

const (
	// Version of the envelope format this build writes and reads.
	Version = 1

	// DefaultIterations is the PBKDF2 round count Create and ChangePassphrase
	// use (OWASP's current recommendation for PBKDF2-HMAC-SHA256).
	DefaultIterations = 600_000

	// MinIterations is the smallest round count Open accepts. An envelope
	// declaring fewer is treated as damaged: an attacker who rewrites an
	// envelope down to, say, one iteration would otherwise turn a decade-long
	// brute force into an afternoon. See minIterations for the test-only
	// exception.
	MinIterations = 100_000

	// iterationCeilingFactor is how far above the KDF cost this build uses an
	// envelope may still declare. The ceiling exists for the mirror reason of
	// MinIterations: an attacker who rewrites an envelope's iteration count up
	// to, say, 10⁹ makes every unlock burn minutes of CPU in PBKDF2 without
	// failing any other check. A factor of 10 over the current default bounds
	// that worst case to roughly ten normal unlock times, while leaving room
	// for parameter growth across releases. See maxIterations for the
	// test-only exception.
	iterationCeilingFactor = 10

	// KeySize is the derived key length in bytes: 32 bytes = AES-256.
	KeySize = 32

	saltSize  = 16
	nonceSize = 12

	// MaxHintSize caps the clear-text hint. The hint is not a secret, but it
	// must not become a free-form data channel inside an otherwise minimal
	// file.
	MaxHintSize = 256
)

// Algo names stored in the envelope.
const (
	kdfAlgo    = "pbkdf2-sha256"
	cipherAlgo = "aes-256-gcm"
)

// aad is the additional authenticated data bound into every seal. It ties the
// ciphertext to this format: copying raw ciphertext into another structure,
// or "downgrading" an envelope by relabelling its parts, makes authentication
// fail instead of decrypting.
const aad = "tdb-vault-v1"

// ErrWrongPassphrase is returned when the sealed data does not authenticate.
// A wrong passphrase and a damaged file look identical to GCM, and keeping
// them indistinguishable denies an attacker that probe.
var ErrWrongPassphrase = errors.New("vault: wrong passphrase or damaged data")

// iterations is the KDF round count Create and ChangePassphrase use. It is a
// variable only so tests can bring it down (tests set it to 1000 and restore
// it with defer); production code must never touch it, and the package
// default stays DefaultIterations.
var iterations = DefaultIterations

// KDF holds the key-derivation parameters recorded with an envelope.
type KDF struct {
	Algo       string `json:"algo"` // "pbkdf2-sha256"
	Iterations int    `json:"iterations"`
	Salt       []byte `json:"salt"` // encoding/json → base64
}

// Cipher holds the cipher parameters recorded with an envelope.
type Cipher struct {
	Algo  string `json:"algo"` // "aes-256-gcm"
	Nonce []byte `json:"nonce"`
}

// Envelope is the sealed object a settings file stores.
type Envelope struct {
	Version    int    `json:"version"`
	KDF        KDF    `json:"kdf"`
	Cipher     Cipher `json:"cipher"`
	Hint       string `json:"hint,omitempty"`
	Ciphertext []byte `json:"data"`
}

// Key is an unlocked vault key. It remembers the KDF parameters it was derived
// from so Reseal can verify the envelope it is asked to re-seal matches, and
// so outdated parameters can be detected and upgraded.
type Key struct {
	kdf KDF
	key [KeySize]byte
	// live is false once Zero ran or derivation failed; further use is refused.
	live bool
}

// Iterations reports the KDF round count the key was derived with. Callers use
// it to notice parameters that are outdated compared to the current defaults.
func (k *Key) Iterations() int {
	if k == nil {
		return 0
	}
	return k.kdf.Iterations
}

// Zero wipes the key material as far as the Go runtime allows. It cannot stop
// copies made by the garbage collector, but it is the honest best effort: the
// soon-collected struct no longer carries the key.
func (k *Key) Zero() {
	if k == nil {
		return
	}
	k.key = [KeySize]byte{}
	k.kdf = KDF{}
	k.live = false
}

// Create seals plaintext under passphrase: a random salt, a KDF run and an
// AES-256-GCM seal with a fresh nonce. It returns the envelope and the unlocked
// key (for a session that keeps the vault open).
func Create(passphrase, hint string, plaintext []byte) (*Envelope, *Key, error) {
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, nil, fmt.Errorf("vault: generating the salt: %w", err)
	}
	kdf := KDF{Algo: kdfAlgo, Iterations: iterations, Salt: salt}
	key, err := deriveKey(passphrase, kdf)
	if err != nil {
		return nil, nil, err
	}
	env := &Envelope{
		Version: Version,
		KDF:     kdf,
		Cipher:  Cipher{Algo: cipherAlgo},
		Hint:    clampHint(hint),
	}
	if err := seal(env, key, plaintext); err != nil {
		key.Zero()
		return nil, nil, err
	}
	return env, key, nil
}

// Open verifies the envelope parameters and decrypts the payload. An
// unsupported version or algorithm is an explicit error (the parameters should
// be shown to the user); anything that makes authentication fail — the wrong
// passphrase, damaged data, truncated ciphertext, a KDF cost outside the
// window this build accepts — is ErrWrongPassphrase, without saying which of
// the two it was.
func Open(passphrase string, env *Envelope) ([]byte, *Key, error) {
	if env == nil {
		return nil, nil, errors.New("vault: no envelope to open")
	}
	if env.Version != Version {
		return nil, nil, fmt.Errorf("vault: unsupported envelope version %d (this build reads %d)", env.Version, Version)
	}
	if env.KDF.Algo != kdfAlgo {
		return nil, nil, fmt.Errorf("vault: unsupported KDF algorithm %q", env.KDF.Algo)
	}
	if env.Cipher.Algo != cipherAlgo {
		return nil, nil, fmt.Errorf("vault: unsupported cipher algorithm %q", env.Cipher.Algo)
	}
	if len(env.Cipher.Nonce) != nonceSize || len(env.KDF.Salt) < saltSize ||
		env.KDF.Iterations < minIterations() || env.KDF.Iterations > maxIterations() {
		// Damaged, or rewritten to be cheaper or more expensive to attack
		// (an inflated iteration count turns every unlock into minutes of
		// CPU work); report it as damaged data rather than a parameter
		// problem that could invite tampering.
		return nil, nil, ErrWrongPassphrase
	}
	key, err := deriveKey(passphrase, env.KDF)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipherGCM(key)
	if err != nil {
		key.Zero()
		return nil, nil, err
	}
	plaintext, err := gcm.Open(nil, env.Cipher.Nonce, env.Ciphertext, []byte(aad))
	if err != nil {
		key.Zero()
		// GCM fails the same way for a wrong passphrase and for altered
		// ciphertext; do not tell an attacker which happened.
		return nil, nil, ErrWrongPassphrase
	}
	return plaintext, key, nil
}

// Reseal re-encrypts plaintext into the envelope with the same salt and the
// same key but a fresh nonce. This is the save path: re-running the KDF on
// every save would burn hundreds of milliseconds per keystroke or theme
// change, while the key is already in memory. The envelope must belong to the
// key — same salt, iterations and algorithms — so a key can never be dropped
// onto an envelope it was not derived from.
func (e *Envelope) Reseal(k *Key, plaintext []byte) error {
	if k == nil || !k.live {
		return errors.New("vault: resealing with a missing or zeroed key")
	}
	if e == nil {
		return errors.New("vault: resealing an empty envelope")
	}
	if e.Version != Version || e.KDF.Algo != k.kdf.Algo || e.Cipher.Algo != cipherAlgo ||
		e.KDF.Iterations != k.kdf.Iterations || !bytes.Equal(k.kdf.Salt, e.KDF.Salt) {
		return errors.New("vault: the envelope does not match the key it is sealed with")
	}
	return seal(e, k, plaintext)
}

// SetHint replaces the clear-text hint. The hint is not a secret and can be
// changed without the key.
func (e *Envelope) SetHint(hint string) {
	if e == nil {
		return
	}
	e.Hint = clampHint(hint)
}

// ChangePassphrase re-seals the plaintext under a new passphrase: a new salt,
// a fresh KDF run and fresh ciphertext. Re-rolling everything means a guess
// against the old passphrase buys an attacker nothing for the new file, and
// dropping the old salt means the old key material is retired outright.
func (e *Envelope) ChangePassphrase(passphrase, hint string, plaintext []byte) (*Key, error) {
	env, key, err := Create(passphrase, hint, plaintext)
	if err != nil {
		return nil, err
	}
	// The assignment below drops the old ciphertext, nonce and salt — key
	// material all of it. Wipe the retired slices before they are replaced
	// instead of leaving them for the garbage collector to reclaim intact,
	// the same hygiene Key.Zero applies to an unlocked key.
	zeroBytes(e.Ciphertext)
	zeroBytes(e.Cipher.Nonce)
	zeroBytes(e.KDF.Salt)
	*e = *env
	return key, nil
}

// seal encrypts plaintext into env with a fresh nonce. Nonces in AES-GCM may
// never repeat under one key, so every seal —Create, Reseal, save, theme
// change alike — draws a new one from crypto/rand instead of reusing a stored
// value.
func seal(env *Envelope, key *Key, plaintext []byte) error {
	gcm, err := cipherGCM(key)
	if err != nil {
		return err
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("vault: generating the nonce: %w", err)
	}
	env.Cipher.Nonce = nonce
	env.Ciphertext = gcm.Seal(nil, nonce, plaintext, []byte(aad))
	return nil
}

// cipherGCM builds an AES-256-GCM instance from the key bytes.
func cipherGCM(key *Key) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key.key[:])
	if err != nil {
		return nil, fmt.Errorf("vault: creating the cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("vault: creating the AEAD: %w", err)
	}
	return gcm, nil
}

// deriveKey runs the KDF stored in kdf and returns a live key.
func deriveKey(passphrase string, kdf KDF) (*Key, error) {
	if kdf.Algo != kdfAlgo {
		return nil, fmt.Errorf("vault: unsupported KDF algorithm %q", kdf.Algo)
	}
	if kdf.Iterations < minIterations() || kdf.Iterations > maxIterations() {
		// The cost window is enforced here too (not only in Open) so no
		// caller can run the KDF on parameters this build deems damaged.
		return nil, ErrWrongPassphrase
	}
	derived, err := pbkdf2.Key(sha256.New, passphrase, kdf.Salt, kdf.Iterations, KeySize)
	if err != nil {
		return nil, fmt.Errorf("vault: deriving the key: %w", err)
	}
	key := &Key{
		kdf:  KDF{Algo: kdf.Algo, Iterations: kdf.Iterations, Salt: clone(kdf.Salt)},
		live: true,
	}
	copy(key.key[:], derived)
	return key, nil
}

// minIterations is the floor Open accepts. In production it is MinIterations.
// When the test-only KDF cost sits below MinIterations, the floor follows it —
// otherwise every envelope the tests create would be rejected by the very
// floor the tests must also exercise. The protection keeps its meaning either
// way: an envelope markedly cheaper than what this build creates is damaged.
func minIterations() int {
	if iterations < MinIterations {
		return iterations
	}
	return MinIterations
}

// maxIterations is the ceiling Open accepts, the mirror image of
// minIterations. In production it is iterationCeilingFactor× the default,
// i.e. 6 000 000 for the 600 000 default (60× the MinIterations floor): an
// envelope declaring more than that is treated as damaged, so a tampered
// settings file cannot turn the next unlock into minutes of silent PBKDF2
// work.
//
// Like minIterations, it follows the test-only KDF cost down: with the knob
// at 1000 the window is [1000, 10 000], so the envelopes the tests create are
// not rejected by the very ceiling the tests must also exercise. The guard
// below keeps the window non-degenerate no matter how small the knob gets —
// the ceiling is never allowed to fall below the floor, and with the knob at
// the default it can never come anywhere near it. The cost window keeps its
// meaning either way: an envelope markedly more expensive than what this
// build creates is damaged, and rejected before a single round runs.
func maxIterations() int {
	ceiling := iterationCeilingFactor * iterations
	if floor := minIterations(); ceiling < floor {
		return floor
	}
	return ceiling
}

// clampHint trims the hint to MaxHintSize runes so the clear-text part of the
// envelope cannot grow without bound.
func clampHint(hint string) string {
	runes := []rune(hint)
	if len(runes) <= MaxHintSize {
		return hint
	}
	return string(runes[:MaxHintSize])
}

func clone(b []byte) []byte { return append([]byte(nil), b...) }

// zeroBytes wipes a byte slice in place: the honest best effort at removing
// retired key material from memory, like Key.Zero.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
