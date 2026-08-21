package gmail

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

type tokenCipher struct {
	aead cipher.AEAD
}

func newTokenCipher(encodedKey string) (tokenCipher, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedKey))
	if err != nil || len(key) != 32 {
		return tokenCipher{}, errors.New("NIMOTSU_ENCRYPTION_KEY must be a base64-encoded 32-byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return tokenCipher{}, fmt.Errorf("create token cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return tokenCipher{}, fmt.Errorf("create token cipher: %w", err)
	}
	return tokenCipher{aead: aead}, nil
}

func (c tokenCipher) encrypt(plain []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate token nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plain, []byte("nimotsu:gmail-token:v1")), nil
}

func (c tokenCipher) decrypt(encrypted []byte) ([]byte, error) {
	nonceSize := c.aead.NonceSize()
	if len(encrypted) <= nonceSize {
		return nil, errors.New("stored Gmail token is invalid")
	}
	plain, err := c.aead.Open(nil, encrypted[:nonceSize], encrypted[nonceSize:], []byte("nimotsu:gmail-token:v1"))
	if err != nil {
		return nil, errors.New("could not decrypt Gmail token; verify NIMOTSU_ENCRYPTION_KEY")
	}
	return plain, nil
}
