package main

import (
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"log"
	"math"
	"mime/multipart"
	"net/http"
	"sync"
)

type Chunk struct {
	index int64
	data  []byte
}

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
	password := req.FormValue("password")

	fileSize, err := HeadFile(s3Context.client, id)
	if err != nil {
		log.Fatal(err)
	}

	numChunks := int64(math.Ceil(float64(fileSize) / float64(bufferLimit)))
	var writeToResponseIndex int64 = 0

	downloadedChannel := make(chan Chunk, numChunks)
	defer close(downloadedChannel)
	decryptedChannel := make(chan Chunk, numChunks)
	defer close(decryptedChannel)

	for counter := range numChunks {
		go func() {
			log.Printf("Sending download request with counter={%d} (start={%d} and end={%d})", counter, bufferLimit*counter, (bufferLimit*counter)+bufferLimit-1)
			chunk, errDownload := DownloadChunk(s3Context.client, id, bufferLimit*counter, (bufferLimit*counter)+bufferLimit-1)

			if errDownload != nil {
				log.Fatalf("Failed to download chunk from S3: %v", errDownload)
			}

			downloadedChannel <- Chunk{index: counter, data: chunk}
		}()
	}

	chunkBuffer := make(map[int64][]byte)
	var bufferMutex sync.Mutex

	go func() {
		for chunk := range downloadedChannel {
			log.Printf("Found a chunk with index={%d}", chunk.index)
			go func() {
				bytes, err := Decrypt(chunk.data, password)
				if err != nil {
					log.Fatalf("Failed to decrypt chunk: %v", err)
				}

				decryptedChannel <- Chunk{index: chunk.index, data: bytes}
			}()
		}
	}()

	go func() {
		for chunk := range decryptedChannel {
			log.Printf("Found a decrypted chunk with index={%d}", chunk.index)
			bufferMutex.Lock()
			chunkBuffer[chunk.index] = chunk.data
			bufferMutex.Unlock()
		}
	}()

	for writeToResponseIndex < numChunks {
		bufferMutex.Lock()
		if data, ok := chunkBuffer[writeToResponseIndex]; ok {
			_, err := w.Write(data)
			if err != nil {
				log.Fatalf("Failed to write to response writer: %v", err)
			}
			if flusher, ok := w.(http.Flusher); ok {
				log.Printf("Flushing!")
				flusher.Flush()
			}
			delete(chunkBuffer, writeToResponseIndex)
			writeToResponseIndex++
		}
		bufferMutex.Unlock()
	}
}

func HandleEncryptedFileUpload(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "Method not allowed", http.StatusMethodNotAllowed)
	}

	errorMultipart := request.ParseMultipartForm(16 << 20)
	if errorMultipart != nil {
		http.Error(writer, "Error parsing form", http.StatusBadRequest)
	}

	file, _, errorFormFile := request.FormFile("uploadFile")
	if errorFormFile != nil {
		http.Error(writer, "Error retrieving file", http.StatusBadRequest)
	}
	defer func(file multipart.File) {
		err := file.Close()
		if err != nil {
			http.Error(writer, "Error closing file", http.StatusInternalServerError)
		}
	}(file)

	const bufferLimit = 5*1024*1024 + 44 // max buffer size per upload to S3
	password := request.FormValue("password")
	buffer := make([]byte, bufferLimit)

	for {
		n, errorReadBytes := file.Read(buffer)
		log.Printf("Read bytes from input! Size: %d", n)
		if errorReadBytes != nil {
			http.Error(writer, "Error reading file bytes", http.StatusInternalServerError)
			break
		}

		decrypted, errDecrypt := Decrypt(buffer[:n], password)

		if errDecrypt != nil {
			http.Error(writer, "Error decrypting file", http.StatusInternalServerError)
			break
		}

		log.Printf("Writing decrypted buffer! Size: %d", len(decrypted))
		_, err := writer.Write(decrypted)
		if err != nil {
			http.Error(writer, "Failed to write decrypted bytes", http.StatusInternalServerError)
			break
		}

		if errorReadBytes == io.EOF || n < bufferLimit {
			if flusher, ok := writer.(http.Flusher); ok {
				log.Printf("Flushing!")
				flusher.Flush()
			}
			log.Printf("Found EOF, finishing reading encrypted upload!")
			break
		}
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
	fileUUID := uuid.New()
	password := request.FormValue("password")
	buffer := make([]byte, bufferLimit)
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
