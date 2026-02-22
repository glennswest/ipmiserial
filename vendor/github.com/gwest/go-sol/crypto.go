package sol

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

// encryptPayload encrypts payload with AES-CBC-128 using K2 as the key.
// Returns IV (16 bytes) + ciphertext.
func (s *Session) encryptPayload(payload []byte) []byte {
	key := s.k2[:16]

	// Generate random 16-byte IV
	iv := make([]byte, aes.BlockSize)
	rand.Read(iv)

	// Confidentiality pad: total of (payload + pad_bytes + pad_length_byte) must be multiple of 16
	padLen := (aes.BlockSize - ((len(payload) + 1) % aes.BlockSize)) % aes.BlockSize
	padded := make([]byte, len(payload)+padLen+1)
	copy(padded, payload)
	for i := 0; i < padLen; i++ {
		padded[len(payload)+i] = byte(i + 1)
	}
	padded[len(padded)-1] = byte(padLen)

	// AES-CBC encrypt
	block, err := aes.NewCipher(key)
	if err != nil {
		s.logf("AES cipher error: %v", err)
		return nil
	}
	mode := cipher.NewCBCEncrypter(block, iv)
	ciphertext := make([]byte, len(padded))
	mode.CryptBlocks(ciphertext, padded)

	// Return IV + ciphertext
	result := make([]byte, aes.BlockSize+len(ciphertext))
	copy(result, iv)
	copy(result[aes.BlockSize:], ciphertext)
	return result
}

// decryptPayload decrypts an RMCP+ encrypted payload (IV + ciphertext).
// Returns the decrypted payload with confidentiality pad removed.
func (s *Session) decryptPayload(data []byte) ([]byte, error) {
	if len(data) < 2*aes.BlockSize {
		return nil, fmt.Errorf("encrypted payload too short: %d", len(data))
	}

	key := s.k2[:16]
	iv := data[:aes.BlockSize]
	ciphertext := data[aes.BlockSize:]

	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext not block-aligned: %d", len(ciphertext))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	mode := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)

	// Remove confidentiality pad: last byte is pad length
	padLen := int(plaintext[len(plaintext)-1])
	if padLen+1 > len(plaintext) {
		return nil, fmt.Errorf("invalid pad length: %d", padLen)
	}

	return plaintext[:len(plaintext)-padLen-1], nil
}
