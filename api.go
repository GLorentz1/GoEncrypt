package main

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
)

func HandleEncryptedDownload(w http.ResponseWriter, req *http.Request, repository *S3Context, id string) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}

	url := GetPresignedUrl(repository, id)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(w)
	data := map[string]string{"downloadUrl": url}
	errorJsonWrite := encoder.Encode(data)

	if errorJsonWrite != nil {
		http.Error(w, "Error writing presigned url to response writer", http.StatusInternalServerError)
	}
}

func HandlePlainDownload(w http.ResponseWriter, req *http.Request, repository map[string]FileData, id string) {
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

func HandleEncryptedFileUpload(writer http.ResponseWriter, request *http.Request, encryptedFileChannel chan EncryptedFileData) {
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

func HandlePlainFileUpload(writer http.ResponseWriter, request *http.Request, fileChannel chan FileData) {
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

	const bufferLimit = 5 * 1024 * 1024
	bufferSize := bufferLimit
	fileUUID := uuid.New()
	password := request.FormValue("password")
	buffer := make([]byte, bufferSize)
	var counter int32 = 1

	for {
		n, err := file.Read(buffer)

		if err != nil && err != io.EOF {
			http.Error(writer, "Error reading file", http.StatusInternalServerError)
		}

		log.Printf("Sending chunk %d with n {%d} bytes", counter, n)

		fileChannel <- FileData{filename: handler.Filename, password: password, fileUUID: fileUUID,
			bytes: buffer[:n], isLastChunk: err == io.EOF || n < bufferLimit, counter: counter}

		if err == io.EOF || n < bufferLimit {
			log.Printf("Found EOF when counter was %d", counter)
			break
		}

		counter += 1
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(writer)
	data := map[string]uuid.UUID{"id": fileUUID}
	errorJsonWrite := encoder.Encode(data)

	if errorJsonWrite != nil {
		http.Error(writer, "Error writing JSON response", http.StatusInternalServerError)
	}
}
