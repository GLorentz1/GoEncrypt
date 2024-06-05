package encryption

import (
	"GoEncryptApi/types"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"golang.org/x/crypto/pbkdf2"
	"io"
)

func Encrypt(fileData types.FileData, encryptedFileChannel chan types.FileData) {

	salt := make([]byte, 16)
	_, err := io.ReadFull(rand.Reader, salt)

	if err != nil {
		return
	}

	key := pbkdf2.Key([]byte(fileData.Password), salt, 2048, 32, sha256.New)

	block, err := aes.NewCipher(key)
	if err != nil {
		return
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return
	}

	nonce := make([]byte, aesGCM.NonceSize())
	_, err = io.ReadFull(rand.Reader, nonce)
	if err != nil {
		return
	}

	ciphertext := aesGCM.Seal(nonce, nonce, fileData.Bytes, nil)
	ciphertext = append(salt, ciphertext...)

	encryptedData := types.FileData{Filename: fileData.Filename, FileUUID: fileData.FileUUID,
		Bytes: ciphertext, IsLastChunk: fileData.IsLastChunk, Counter: fileData.Counter}

	encryptedFileChannel <- encryptedData
}

func Decrypt(chunk []byte, password string) ([]byte, error) {
	salt := chunk[:16]

	key := pbkdf2.Key([]byte(password), salt, 2048, 32, sha256.New)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := aesGCM.NonceSize()
	nonce, ciphertext := chunk[16:16+nonceSize], chunk[16+nonceSize:]

	originalBytes, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	return originalBytes, nil
}
