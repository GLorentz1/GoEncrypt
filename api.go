package main

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

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
