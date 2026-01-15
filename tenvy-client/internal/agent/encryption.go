package agent

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
)

func (a *Agent) encrypt(plaintext []byte) ([]byte, error) {
	if len(a.ecdhSharedSecret) == 0 {
		return nil, errors.New("no shared secret available for encryption")
	}

	key := sha256.Sum256(a.ecdhSharedSecret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	iv := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}

	n, err := rand.Int(rand.Reader, big.NewInt(256))
	if err != nil {
		return nil, err
	}
	paddingLen := uint16(n.Int64() + 1)
	padding := make([]byte, paddingLen)
	if _, err := io.ReadFull(rand.Reader, padding); err != nil {
		return nil, err
	}

	paddingLenBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(paddingLenBuf, paddingLen)

	ciphertext := gcm.Seal(nil, iv, plaintext, nil)
	tag := ciphertext[len(ciphertext)-16:]
	actualCiphertext := ciphertext[:len(ciphertext)-16]

	packet := make([]byte, 0, len(iv)+16+2+len(padding)+len(actualCiphertext))
	packet = append(packet, iv...)
	packet = append(packet, tag...)
	packet = append(packet, paddingLenBuf...)
	packet = append(packet, padding...)
	packet = append(packet, actualCiphertext...)

	return packet, nil
}

func (a *Agent) decrypt(packet []byte) ([]byte, error) {
	if len(a.ecdhSharedSecret) == 0 {
		return nil, errors.New("no shared secret available for decryption")
	}

	const minHeader = 12 + 16 + 2
	if len(packet) < minHeader {
		return nil, errors.New("packet too short")
	}

	key := sha256.Sum256(a.ecdhSharedSecret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	iv := packet[:12]
	tag := packet[12:28]
	paddingLen := binary.BigEndian.Uint16(packet[28:30])

	ciphertextStart := 30 + int(paddingLen)
	if len(packet) < ciphertextStart {
		return nil, errors.New("packet smaller than indicated padding")
	}

	actualCiphertext := packet[ciphertextStart:]

	combined := make([]byte, len(actualCiphertext)+16)
	copy(combined, actualCiphertext)
	copy(combined[len(actualCiphertext):], tag)

	return gcm.Open(nil, iv, combined, nil)
}
