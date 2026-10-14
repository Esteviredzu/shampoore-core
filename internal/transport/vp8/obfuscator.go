package vp8

import "crypto/sha256"

type Obfuscator struct {
	key []byte
}

func NewObfuscator(secret string) *Obfuscator {
	if secret == "" {
		return &Obfuscator{}
	}
	hash := sha256.Sum256([]byte(secret))
	return &Obfuscator{key: hash[:]}
}

func (o *Obfuscator) Obfuscate(data []byte) []byte {
	if len(o.key) == 0 {
		out := make([]byte, len(data))
		copy(out, data)
		return out
	}
	out := make([]byte, len(data))
	for i := range data {
		out[i] = data[i] ^ o.key[i%len(o.key)]
	}
	return out
}

func (o *Obfuscator) Deobfuscate(data []byte) []byte {
	return o.Obfuscate(data)
}