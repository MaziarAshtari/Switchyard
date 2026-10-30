// Package secret encrypts secrets at rest and hashes passwords and tokens.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

const prefix = "v1:"

// ErrDecrypt is returned when a value cannot be decrypted (wrong key or corrupt data).
var ErrDecrypt = errors.New("secret: cannot decrypt value (wrong ENCRYPTION_KEY?)")

// Box encrypts values with AES-256-GCM and fingerprints them with HMAC-SHA256.
// Both keys are derived from the configured ENCRYPTION_KEY.
type Box struct {
	aead   cipher.AEAD
	macKey []byte
}

// NewBox derives the encryption and MAC keys from key.
func NewBox(key string) (*Box, error) {
	encKey, err := hkdf.Key(sha256.New, []byte(key), nil, "switchyard aes-gcm v1", 32)
	if err != nil {
		return nil, err
	}
	macKey, err := hkdf.Key(sha256.New, []byte(key), nil, "switchyard hmac v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead, macKey: macKey}, nil
}

// Encrypt returns "v1:" + base64(nonce || ciphertext). The empty string stays empty.
func (b *Box) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt.
func (b *Box) Decrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	raw, ok := strings.CutPrefix(value, prefix)
	if !ok {
		return "", ErrDecrypt
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(data) < b.aead.NonceSize() {
		return "", ErrDecrypt
	}
	n := b.aead.NonceSize()
	plain, err := b.aead.Open(nil, data[:n], data[n:], nil)
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plain), nil
}

// Fingerprint returns a keyed hash of value, used to detect duplicates
// without storing the value in clear text.
func (b *Box) Fingerprint(value string) string {
	m := hmac.New(sha256.New, b.macKey)
	m.Write([]byte(value))
	return hex.EncodeToString(m.Sum(nil))
}

// HashToken returns the SHA-256 hex digest of a high-entropy token
// (gateway keys, session tokens).
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewToken returns prefix followed by 2*n random hex characters.
func NewToken(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return prefix + hex.EncodeToString(b)
}

// Mask returns a display form of a secret that reveals only a few characters.
func Mask(s string) string {
	s = strings.TrimSpace(s)
	switch n := len(s); {
	case n >= 16:
		return s[:3] + "..." + s[n-4:]
	case n >= 8:
		return "..." + s[n-3:]
	default:
		return "..."
	}
}
