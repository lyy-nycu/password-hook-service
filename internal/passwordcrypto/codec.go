package passwordcrypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const AlgorithmAES256GCM = "AES-256-GCM"

type Envelope struct {
	Ciphertext string
	Nonce      string
	KeyID      string
	Algorithm  string
}

type Codec struct {
	aead  cipher.AEAD
	keyID string
}

func NewCodecFromBase64(keyB64 string, keyID string) (*Codec, error) {
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("decode password encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("password encryption key must decode to 32 bytes, got %d", len(key))
	}
	if keyID == "" {
		return nil, errors.New("password encryption key id is required")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	return &Codec{aead: aead, keyID: keyID}, nil
}

func (c *Codec) Encrypt(ctx context.Context, plaintext []byte, aad []byte) (Envelope, error) {
	if err := ctx.Err(); err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Envelope{}, fmt.Errorf("generate password nonce: %w", err)
	}
	ciphertext := c.aead.Seal(nil, nonce, plaintext, aad)
	return Envelope{
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		KeyID:      c.keyID,
		Algorithm:  AlgorithmAES256GCM,
	}, nil
}

func (c *Codec) Decrypt(ctx context.Context, env Envelope, aad []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if env.Algorithm != AlgorithmAES256GCM {
		return nil, fmt.Errorf("unsupported password encryption algorithm %q", env.Algorithm)
	}
	if env.KeyID != c.keyID {
		return nil, fmt.Errorf("unsupported password encryption key id %q", env.KeyID)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode password nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode password ciphertext: %w", err)
	}
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, errors.New("decrypt password payload failed")
	}
	return plaintext, nil
}

func ZeroBytes(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}
