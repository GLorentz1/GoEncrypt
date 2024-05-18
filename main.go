package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"golang.org/x/crypto/pbkdf2"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
)

type FileData struct {
	filename string
	password string
	fileUUID uuid.UUID
	bytes    []byte
}

type EncryptedFileData struct {
	filename string
	password string
	fileUUID uuid.UUID
	bytes    []byte
}

func main() {
	var wg sync.WaitGroup
	router := mux.NewRouter()

	fileChannel := make(chan FileData, 10)
	encryptedFileChannel := make(chan EncryptedFileData, 10)

	encryptedDataRepository := make(map[string]EncryptedFileData, 10)
	plainDataRepository := make(map[string]FileData, 10)

	router.HandleFunc("/encrypt", func(writer http.ResponseWriter, request *http.Request) {
		handlePlainFileUpload(writer, request, fileChannel)
	})

	router.HandleFunc("/decrypt", func(writer http.ResponseWriter, request *http.Request) {
		handleEncryptedFileUpload(writer, request, encryptedFileChannel)
	})

	router.HandleFunc("/download/encrypted/{uuid}", func(writer http.ResponseWriter, request *http.Request) {
		uuidFromRequest := mux.Vars(request)["uuid"]
		handleEncryptedDownload(writer, request, encryptedDataRepository, uuidFromRequest)
	})

	router.HandleFunc("/download/decrypted/{uuid}", func(writer http.ResponseWriter, request *http.Request) {
		uuidFromRequest := mux.Vars(request)["uuid"]
		handlePlainDownload(writer, request, plainDataRepository, uuidFromRequest)
	})

	for range 5 {
		wg.Add(1)
		go encryptFile(fileChannel, encryptedDataRepository)
	}

	for range 5 {
		wg.Add(1)
		go decryptFile(encryptedFileChannel, plainDataRepository)
	}

	errListenAndServe := http.ListenAndServe(":5678", router)
	if errListenAndServe != nil {
		panic(errListenAndServe)
	}

	wg.Wait()
}

func handleEncryptedDownload(w http.ResponseWriter, req *http.Request, repository map[string]EncryptedFileData, id string) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}

	data := repository[id]
	if data.bytes == nil {
		w.WriteHeader(http.StatusAccepted)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", "encrypted_"+data.filename+".enc"))
		w.WriteHeader(http.StatusOK)
		_, err := w.Write(data.bytes)

		if err != nil {
			http.Error(w, "Error writing encrypted file to response writer", http.StatusInternalServerError)
		}
	}

}

func handlePlainDownload(w http.ResponseWriter, req *http.Request, repository map[string]FileData, id string) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}

	data := repository[id]
	if data.bytes == nil {
		w.WriteHeader(http.StatusAccepted)
	} else {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", strings.TrimSuffix(data.filename, ".enc")))
		w.WriteHeader(http.StatusOK)
		_, err := w.Write(data.bytes)

		if err != nil {
			http.Error(w, "Error writing plain file to response writer", http.StatusInternalServerError)
		}
	}

}

func handleEncryptedFileUpload(writer http.ResponseWriter, request *http.Request, encryptedFileChannel chan EncryptedFileData) {
	if request.Method != http.MethodPost {
		http.Error(writer, "Method not allowed", http.StatusMethodNotAllowed)
	}

	errorMultipart := request.ParseMultipartForm(16 << 20)
	if errorMultipart != nil {
		http.Error(writer, "Error parsing form", http.StatusBadRequest)
	}

	file, handler, errorFormFile := request.FormFile("uploadFile")
	if errorFormFile != nil {
		http.Error(writer, "Error retrieving file", http.StatusBadRequest)
	}
	defer func(file multipart.File) {
		err := file.Close()
		if err != nil {
			http.Error(writer, "Error closing file", http.StatusInternalServerError)
		}
	}(file)

	fileBytes, errorReadBytes := io.ReadAll(file)
	if errorReadBytes != nil {
		http.Error(writer, "Error reading file bytes", http.StatusInternalServerError)
	}

	fileUUID := uuid.New()

	password := request.FormValue("password")
	encryptedFileChannel <- EncryptedFileData{filename: handler.Filename, password: password, fileUUID: fileUUID, bytes: fileBytes}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(writer)
	data := map[string]uuid.UUID{"id": fileUUID}
	errorJsonWrite := encoder.Encode(data)

	if errorJsonWrite != nil {
		http.Error(writer, "Error writing JSON response", http.StatusInternalServerError)
	}
}

func handlePlainFileUpload(writer http.ResponseWriter, request *http.Request, fileChannel chan FileData) {
	if request.Method != http.MethodPost {
		http.Error(writer, "Method not allowed", http.StatusMethodNotAllowed)
	}

	errorMultipart := request.ParseMultipartForm(16 << 20)
	if errorMultipart != nil {
		http.Error(writer, "Error parsing form", http.StatusBadRequest)
	}

	file, handler, errorFormFile := request.FormFile("uploadFile")
	if errorFormFile != nil {
		http.Error(writer, "Error retrieving file", http.StatusBadRequest)
	}
	defer func(file multipart.File) {
		err := file.Close()
		if err != nil {
			http.Error(writer, "Error closing file", http.StatusInternalServerError)
		}
	}(file)

	fileBytes, errorReadBytes := io.ReadAll(file)
	if errorReadBytes != nil {
		http.Error(writer, "Error reading file bytes", http.StatusInternalServerError)
	}

	fileUUID := uuid.New()

	password := request.FormValue("password")
	fileChannel <- FileData{filename: handler.Filename, password: password, fileUUID: fileUUID, bytes: fileBytes}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(writer)
	data := map[string]uuid.UUID{"id": fileUUID}
	errorJsonWrite := encoder.Encode(data)

	if errorJsonWrite != nil {
		http.Error(writer, "Error writing JSON response", http.StatusInternalServerError)
	}
}

func encryptFile(fileDataChannel chan FileData, repository map[string]EncryptedFileData) {

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

		repository[fileData.fileUUID.String()] =
			EncryptedFileData{filename: fileData.filename, fileUUID: fileData.fileUUID, bytes: ciphertext}
	}

}

func decryptFile(encryptedFileChannel chan EncryptedFileData, repository map[string]FileData) {
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
