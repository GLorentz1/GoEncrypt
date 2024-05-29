package main

import (
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"log"
	"mime/multipart"
	"net/http"
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

func HandlePlainDownload(w http.ResponseWriter, req *http.Request, s3Context *S3Context, id string) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}

	const bufferLimit = 5*1024*1024 + 44 // max buffer size per upload to S3
	var start int64 = 0
	password := req.FormValue("password")

	for {
		chunk, err := DownloadChunk(s3Context.service, id, start, start+bufferLimit-1)

		if err != nil {
			log.Printf("Failed to download chunk from S3: %v", err)
		}

		bytes, err := Decrypt(chunk, password)
		if err != nil {
			log.Printf("Failed to decrypt chunk: %v", err)
		}

		_, errWrite := w.Write(bytes)
		if errWrite != nil {
			log.Printf("Failed to write to response writer")
		}

		if len(chunk) < bufferLimit-1 {
			log.Printf("Finished writing to response writer. len(bytes)={%d} < {%d}", len(chunk), bufferLimit-1)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			break
		}

		start += bufferLimit
	}
}

func HandleEncryptedFileUpload(writer http.ResponseWriter, request *http.Request, encryptedFileChannel chan FileData) {
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
	encryptedFileChannel <- FileData{filename: handler.Filename, password: password, fileUUID: fileUUID, bytes: fileBytes}

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

		data := make([]byte, n)
		copy(data, buffer[:n])

		fileChannel <- FileData{filename: handler.Filename, password: password, fileUUID: fileUUID,
			bytes: data, isLastChunk: err == io.EOF || n < bufferLimit, counter: counter}

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
