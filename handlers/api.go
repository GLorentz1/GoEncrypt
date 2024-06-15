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
)

type Chunk struct {
	index int64
	data  []byte
}

func HandleHome(w http.ResponseWriter, req *http.Request) {
	_ = views.Home().Render(req.Context(), w)
}

func HandleEncryptedDownload(w http.ResponseWriter, req *http.Request, repository *aws.S3Context, id string) {
	if req.Method != http.MethodGet {
		_ = views.DownloadEncrypted("", types.Response{Status: http.StatusMethodNotAllowed, Msg: "Method not allowed"}).Render(req.Context(), w)
		return
	}

	_, err := aws.HeadFile(repository.Client, id)
	if err != nil {
		_ = views.DownloadEncrypted("", types.Response{Status: http.StatusAccepted, Msg: "No file found with the supplied id. If your id is correct and not expired, try again in a few minutes because your file might still be processing."}).Render(req.Context(), w)
		return
	}

	url, err := aws.GetPresignedUrl(repository, id)

	if err != nil {
		_ = views.DownloadEncrypted("", types.Response{Status: http.StatusAccepted, Msg: "No file found with the supplied id. If your id is correct and not expired, try again in a few minutes because your file might still be processing."}).Render(req.Context(), w)
		return
	}

	_ = views.DownloadEncrypted(url, types.Response{Status: http.StatusOK}).Render(req.Context(), w)
}

func handlePlainDownload(w http.ResponseWriter, req *http.Request, s3Context *aws.S3Context, id string) {
	const bufferLimit = 5*1024*1024 + 44 // max buffer size per upload to S3
	password := req.FormValue("password")

	fileSize, err := aws.HeadFile(s3Context.Client, id)
	if err != nil {
		http.Error(w, "Couldn't find file with the provided id.", http.StatusBadRequest)
		return
	}

	numChunks := int64(math.Ceil(float64(fileSize) / float64(bufferLimit)))

	for counter := range numChunks {
		chunk, errDownload := aws.DownloadChunk(s3Context.Client, id, bufferLimit*counter, (bufferLimit*counter)+bufferLimit-1)

		if errDownload != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		bytes, err := encryption.Decrypt(chunk, password)
		if err != nil {
			http.Error(w, "Failed to decrypt file. Is your password correct?", http.StatusBadRequest)
			return
		}

		_, err = w.Write(bytes)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

	}
}

func HandleDecryption(writer http.ResponseWriter, request *http.Request, s3Context *aws.S3Context) {
	if request.Method != http.MethodPost {
		_ = views.Decryption(types.Response{Status: http.StatusMethodNotAllowed, Msg: "Method not allowed"}).Render(request.Context(), writer)
		return
	}

	id := request.FormValue("fileId")

	if id != "" {
		handlePlainDownload(writer, request, s3Context, id)
	} else {
		errorMultipart := request.ParseMultipartForm(16 << 20)
		if errorMultipart != nil {
			http.Error(writer, "Bad request.", http.StatusBadRequest)
			return
		}

		file, _, errorFormFile := request.FormFile("uploadFile")
		if errorFormFile != nil {
			http.Error(writer, "Bad request.", http.StatusBadRequest)
			return
		}
		defer func(file multipart.File) {
			err := file.Close()
			if err != nil {
				http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
				return
			}
		}(file)

		const bufferLimit = 5*1024*1024 + 44 // max buffer size per upload to S3
		password := request.FormValue("password")
		buffer := make([]byte, bufferLimit)

		for {
			n, errorReadBytes := file.Read(buffer)

			if errorReadBytes != nil {
				http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			decrypted, errDecrypt := encryption.Decrypt(buffer[:n], password)

			if errDecrypt != nil {
				http.Error(writer, "Failed to decrypt file. Is your password correct?", http.StatusBadRequest)
				return
			}

			_, err := writer.Write(decrypted)
			if err != nil {
				http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			if errorReadBytes == io.EOF || n < bufferLimit {
				if flusher, ok := writer.(http.Flusher); ok {
					flusher.Flush()
				}
				break
			}
		}
	}
}

func HandleEncryption(writer http.ResponseWriter, request *http.Request, fileChannel chan types.FileData) {
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
