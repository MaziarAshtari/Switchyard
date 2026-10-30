package secret

import (
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	b, err := NewBox("a-long-test-encryption-key")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := b.Encrypt("sk-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "v1:") || strings.Contains(enc, "sk-secret") {
		t.Fatalf("unexpected ciphertext %q", enc)
	}
	enc2, _ := b.Encrypt("sk-secret-value")
	if enc == enc2 {
		t.Fatal("expected random nonces to produce different ciphertexts")
	}
	got, err := b.Decrypt(enc)
	if err != nil || got != "sk-secret-value" {
		t.Fatalf("Decrypt = %q, %v", got, err)
	}
	if e, _ := b.Encrypt(""); e != "" {
		t.Fatalf("empty value should stay empty, got %q", e)
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	b1, _ := NewBox("first-key-0123456789")
	b2, _ := NewBox("second-key-0123456789")
	enc, _ := b1.Encrypt("hello")
	if _, err := b2.Decrypt(enc); err != ErrDecrypt {
		t.Fatalf("expected ErrDecrypt, got %v", err)
	}
	if _, err := b1.Decrypt("garbage"); err != ErrDecrypt {
		t.Fatalf("expected ErrDecrypt for garbage, got %v", err)
	}
}

func TestFingerprintIsStableAndKeyed(t *testing.T) {
	b1, _ := NewBox("first-key-0123456789")
	b2, _ := NewBox("second-key-0123456789")
	if b1.Fingerprint("x") != b1.Fingerprint("x") {
		t.Fatal("fingerprint not stable")
	}
	if b1.Fingerprint("x") == b2.Fingerprint("x") {
		t.Fatal("fingerprint should depend on the key")
	}
}

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "correct horse") {
		t.Fatal("password should match")
	}
	if CheckPassword(h, "wrong") || CheckPassword("bogus", "correct horse") {
		t.Fatal("wrong password or hash should not match")
	}
}

func TestMask(t *testing.T) {
	cases := map[string]string{
		"sk-abcdefghijklmnop1234": "sk-...1234",
		"abcdefghij":              "...hij",
		"short":                   "...",
	}
	for in, want := range cases {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
}
