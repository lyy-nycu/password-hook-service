package passwordcrypto

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestCodecEncryptsWithoutPlaintextAndDecrypts(t *testing.T) {
	t.Parallel()

	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	codec, err := NewCodecFromBase64(key, "password-payload-key-v1")
	if err != nil {
		t.Fatalf("NewCodecFromBase64 returned error: %v", err)
	}

	env, err := codec.Encrypt(context.Background(), []byte("cleartext-password"), []byte("cn=u123;upn=u123@example.edu"))
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}

	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if strings.Contains(string(body), "cleartext-password") {
		t.Fatalf("encrypted envelope contains plaintext: %s", body)
	}
	if env.Ciphertext == "" || env.Nonce == "" || env.KeyID != "password-payload-key-v1" || env.Algorithm != AlgorithmAES256GCM {
		t.Fatalf("unexpected envelope: %#v", env)
	}

	plaintext, err := codec.Decrypt(context.Background(), env, []byte("cn=u123;upn=u123@example.edu"))
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}
	if string(plaintext) != "cleartext-password" {
		t.Fatalf("plaintext = %q, want cleartext-password", plaintext)
	}
}

func TestCodecRejectsWrongKey(t *testing.T) {
	t.Parallel()

	keyA := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	keyB := base64.StdEncoding.EncodeToString([]byte("abcdef0123456789abcdef0123456789"))
	codecA, err := NewCodecFromBase64(keyA, "password-payload-key-v1")
	if err != nil {
		t.Fatalf("NewCodecFromBase64 A returned error: %v", err)
	}
	codecB, err := NewCodecFromBase64(keyB, "password-payload-key-v1")
	if err != nil {
		t.Fatalf("NewCodecFromBase64 B returned error: %v", err)
	}

	env, err := codecA.Encrypt(context.Background(), []byte("cleartext-password"), []byte("aad"))
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}
	if _, err := codecB.Decrypt(context.Background(), env, []byte("aad")); err == nil || err.Error() != "decrypt password payload failed" {
		t.Fatalf("Decrypt error = %v, want decrypt password payload failed", err)
	}
}

func TestNewCodecRejectsInvalidKey(t *testing.T) {
	t.Parallel()

	if _, err := NewCodecFromBase64(base64.StdEncoding.EncodeToString([]byte("short")), "password-payload-key-v1"); err == nil {
		t.Fatal("NewCodecFromBase64 returned nil error for short key")
	}
	if _, err := NewCodecFromBase64("not-base64", "password-payload-key-v1"); err == nil {
		t.Fatal("NewCodecFromBase64 returned nil error for invalid base64")
	}
	if _, err := NewCodecFromBase64(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), ""); err == nil || err.Error() != "password encryption key id is required" {
		t.Fatalf("NewCodecFromBase64 error = %v, want key id required", err)
	}
}

func TestDecryptRejectsWrongAlgorithm(t *testing.T) {
	t.Parallel()

	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	codec, err := NewCodecFromBase64(key, "password-payload-key-v1")
	if err != nil {
		t.Fatalf("NewCodecFromBase64 returned error: %v", err)
	}

	env, err := codec.Encrypt(context.Background(), []byte("cleartext-password"), nil)
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}
	env.Algorithm = "AES-128-CBC"

	if _, err := codec.Decrypt(context.Background(), env, nil); err == nil || !strings.Contains(err.Error(), "unsupported password encryption algorithm") {
		t.Fatalf("Decrypt error = %v, want unsupported algorithm", err)
	}
}

func TestZeroBytesClearsBuffer(t *testing.T) {
	t.Parallel()

	buf := []byte("cleartext-password")
	ZeroBytes(buf)
	for i, b := range buf {
		if b != 0 {
			t.Fatalf("buf[%d] = %d, want 0", i, b)
		}
	}
}
