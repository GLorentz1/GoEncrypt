package handlers

import (
	"GoEncryptApi/aws"
	"GoEncryptApi/encryption"
	"GoEncryptApi/types"
	"GoEncryptApi/views"
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

func HandleHome(w http.ResponseWriter, req *http.Request) {
	err := views.Home().Render(req.Context(), w)
	if err != nil {
		log.Print(err)
	}
}

func HandleEncryptedDownload(w http.ResponseWriter, req *http.Request, repository *aws.S3Context, id string) {
	if req.Method != http.MethodGet {
		views.DownloadEncrypted("", types.Response{Status: http.StatusMethodNotAllowed, Msg: "Method not allowed"}).Render(req.Context(), w)
		return
	}

	_, err := aws.HeadFile(repository.Client, id)
	if err != nil {
		views.DownloadEncrypted("", types.Response{Status: http.StatusAccepted, Msg: "Your file is still being processed. Try again in a few minutes."}).Render(req.Context(), w)
		return
	}

	url, err := aws.GetPresignedUrl(repository, id)

	if err != nil {
		views.DownloadEncrypted("", types.Response{Status: http.StatusAccepted, Msg: "Your file is still being processed. Try again in a few minutes."}).Render(req.Context(), w)
		return
	}

	_ = views.DownloadEncrypted(url, types.Response{Status: http.StatusOK}).Render(req.Context(), w)
}

func HandlePlainDownload(w http.ResponseWriter, req *http.Request, s3Context *aws.S3Context, id string) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}

	const bufferLimit = 5*1024*1024 + 44 // max buffer size per upload to S3
	password := req.FormValue("password")

	fileSize, err := aws.HeadFile(s3Context.Client, id)
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
			chunk, errDownload := aws.DownloadChunk(s3Context.Client, id, bufferLimit*counter, (bufferLimit*counter)+bufferLimit-1)

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
				bytes, err := encryption.Decrypt(chunk.data, password)
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

		decrypted, errDecrypt := encryption.Decrypt(buffer[:n], password)

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

func HandlePlainFileUpload(writer http.ResponseWriter, request *http.Request, fileChannel chan types.FileData) {
	if request.Method != http.MethodPost {
		views.Encrypted(uuid.UUID{}, types.Response{Status: http.StatusMethodNotAllowed, Msg: "Method not allowed"}).Render(request.Context(), writer)
		return
	}

	errorMultipart := request.ParseMultipartForm(16 << 20)
	if errorMultipart != nil {
		views.Encrypted(uuid.UUID{}, types.Response{Status: http.StatusBadRequest, Msg: "Error reading file"}).Render(request.Context(), writer)
		return
	}

	file, handler, errorFormFile := request.FormFile("uploadFile")
	if errorFormFile != nil {
		views.Encrypted(uuid.UUID{}, types.Response{Status: http.StatusBadRequest, Msg: "Error reading file"}).Render(request.Context(), writer)
		return
	}
	defer func(file multipart.File) {
		err := file.Close()
		if err != nil {
			views.Encrypted(uuid.UUID{}, types.Response{Status: http.StatusBadRequest, Msg: "Error reading file"}).Render(request.Context(), writer)
			return
		}
	}(file)

	const bufferLimit = 5 * 1024 * 1024
	const sizeLimit = 64 * 1024 * 1024
	var currentSize = 0
	fileUUID := uuid.New()
	password := request.FormValue("password")

	buffer := make([]byte, bufferLimit)
	var counter int32 = 1

	for {
		n, err := file.Read(buffer)

		if err != nil && err != io.EOF {
			views.Encrypted(uuid.UUID{}, types.Response{Status: http.StatusBadRequest, Msg: "Error reading file"}).Render(request.Context(), writer)
			return
		}

		data := make([]byte, n)
		copy(data, buffer[:n])

		fileChannel <- types.FileData{Filename: handler.Filename, Password: password, FileUUID: fileUUID,
			Bytes: data, IsLastChunk: err == io.EOF || n < bufferLimit, Counter: counter}

		if err == io.EOF || n < bufferLimit {
			break
		}

		counter += 1
		currentSize += n
		if currentSize >= sizeLimit {
			views.Encrypted(uuid.UUID{}, types.Response{Status: http.StatusBadRequest, Msg: "File is too big. Size limit: 64MB"}).Render(request.Context(), writer)
			return
		}
	}

	err := views.Encrypted(fileUUID, types.Response{Status: http.StatusOK}).Render(request.Context(), writer)
	if err != nil {
		log.Print(err)
	}
}
