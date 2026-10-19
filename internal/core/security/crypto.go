package security

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	NonceSize = chacha20poly1305.NonceSizeX
	TagSize   = 16
	KeySize   = 32
)

var (
	hkdfSalt = []byte("yandex-board-vpn/v1/secure-pipe")
	hkdfInfo = []byte("xchacha20poly1305")
)

func DeriveKey(password string) ([]byte, error) {
	if password == "" {
		return nil, errors.New("password must be non-empty")
	}
	r := hkdf.New(sha256.New, []byte(password), hkdfSalt, hkdfInfo)
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, err
	}
	return key, nil
}

func Encrypt(key, plaintext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(nonce)+len(plaintext)+aead.Overhead())
	out = append(out, nonce...)
	out = aead.Seal(out, nonce, plaintext, nil)
	return out, nil
}

func Decrypt(key, payload []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	ns := aead.NonceSize()
	if len(payload) < ns+aead.Overhead() {
		return nil, errors.New("ciphertext too short")
	}
	nonce := payload[:ns]
	ct := payload[ns:]
	return aead.Open(nil, nonce, ct, nil)
}