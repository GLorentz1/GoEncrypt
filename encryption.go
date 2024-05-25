package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"golang.org/x/crypto/pbkdf2"
	"io"
)

func EncryptFile(fileDataChannel chan FileData, s3Context *S3Context) {

	for fileData := range fileDataChannel {
		salt := make([]byte, 16)
		_, err := io.ReadFull(rand.Reader, salt)

		if err != nil {
			continue
		}

		key := pbkdf2.Key([]byte(fileData.password), salt, 2048, 32, sha256.New)

		block, err := aes.NewCipher(key)
		if err != nil {
			continue
		}

		aesGCM, err := cipher.NewGCM(block)
		if err != nil {
			continue
		}

		nonce := make([]byte, aesGCM.NonceSize())
		_, err = io.ReadFull(rand.Reader, nonce)
		if err != nil {
			continue
		}

		ciphertext := aesGCM.Seal(nonce, nonce, fileData.bytes, nil)
		ciphertext = append(salt, ciphertext...)

		encryptedData := EncryptedFileData{filename: fileData.filename, fileUUID: fileData.fileUUID,
			bytes: ciphertext, isLastChunk: fileData.isLastChunk, counter: fileData.counter}

		go UploadPart(s3Context, encryptedData)
	}

}

func DecryptFile(encryptedFileChannel chan EncryptedFileData, repository map[string]FileData) {
	for encryptedFile := range encryptedFileChannel {
		ciphertext := encryptedFile.bytes
		salt := ciphertext[:16]

		key := pbkdf2.Key([]byte(encryptedFile.password), salt, 2048, 32, sha256.New)

		block, err := aes.NewCipher(key)
		if err != nil {
			continue
		}

		aesGCM, err := cipher.NewGCM(block)
		if err != nil {
			continue
		}

		nonceSize := aesGCM.NonceSize()
		nonce, ciphertext := ciphertext[16:16+nonceSize], ciphertext[16+nonceSize:]

		originalBytes, err := aesGCM.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			continue
		}

		repository[encryptedFile.fileUUID.String()] =
			FileData{filename: encryptedFile.filename, fileUUID: encryptedFile.fileUUID, bytes: originalBytes}
	}
}
