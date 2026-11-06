// Package crypto encrypts secrets saved from the Settings page (API tokens, OAuth refresh tokens)
// with AES-256-GCM. The blob format "v1.<iv>.<tag>.<data>" (base64url, no padding) is shared with
// earlier versions, so saved secrets keep working.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"strings"
)

type Cipher struct{ key [32]byte }

// New derives the key from ENCRYPTION_KEY (or the dashboard token).
func New(secret string) *Cipher { return &Cipher{key: sha256.Sum256([]byte("pm-assistant:" + secret))} }

var b64 = base64.RawURLEncoding

func (c *Cipher) aead() cipher.AEAD {
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		panic(err)
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return g
}

func (c *Cipher) Encrypt(plain string) string {
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		panic(err)
	}
	sealed := c.aead().Seal(nil, iv, []byte(plain), nil)
	data, tag := sealed[:len(sealed)-16], sealed[len(sealed)-16:]
	return strings.Join([]string{"v1", b64.EncodeToString(iv), b64.EncodeToString(tag), b64.EncodeToString(data)}, ".")
}

func (c *Cipher) Decrypt(blob string) (string, bool) {
	if blob == "" {
		return "", false
	}
	parts := strings.Split(blob, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return "", false
	}
	iv, e1 := b64.DecodeString(parts[1])
	tag, e2 := b64.DecodeString(parts[2])
	data, e3 := b64.DecodeString(parts[3])
	if e1 != nil || e2 != nil || e3 != nil || len(iv) != 12 || len(tag) != 16 {
		slog.Error("decrypt failed: ENCRYPTION_KEY changed? re-enter the secret in Settings")
		return "", false
	}
	plain, err := c.aead().Open(nil, iv, append(data, tag...), nil)
	if err != nil {
		slog.Error("decrypt failed: ENCRYPTION_KEY changed? re-enter the secret in Settings")
		return "", false
	}
	return string(plain), true
}

// Last4 is the tail shown in Settings instead of a secret.
func Last4(s string) string {
	r := []rune(s)
	if len(r) <= 4 {
		return s
	}
	return string(r[len(r)-4:])
}

// RandomID returns n random bytes as hex.
func RandomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
