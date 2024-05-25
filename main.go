package main

import (
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"net/http"
	"sync"
)

type FileData struct {
	filename    string
	password    string
	fileUUID    uuid.UUID
	bytes       []byte
	counter     int32
	isLastChunk bool
}

type EncryptedFileData struct {
	filename    string
	password    string
	fileUUID    uuid.UUID
	bytes       []byte
	counter     int32
	isLastChunk bool
}

func main() {
	var wg sync.WaitGroup
	router := mux.NewRouter()
	s3Context := InitializeS3Client()
	fileChannel := make(chan FileData, 10)
	encryptedFileChannel := make(chan EncryptedFileData, 10)
	plainDataRepository := make(map[string]FileData, 10)

	declareRoutes(router, fileChannel, encryptedFileChannel, &s3Context, plainDataRepository)
	initializeEncryptionWorkers(&wg, fileChannel, &s3Context)
	initializeDecryptionWorkers(&wg, encryptedFileChannel, plainDataRepository)

	errListenAndServe := http.ListenAndServe(":5678", router)
	if errListenAndServe != nil {
		panic(errListenAndServe)
	}

	wg.Wait()
}

func initializeDecryptionWorkers(wg *sync.WaitGroup, encryptedFileChannel chan EncryptedFileData,
	plainDataRepository map[string]FileData) {
	for range 5 {
		wg.Add(1)
		go DecryptFile(encryptedFileChannel, plainDataRepository)
	}
}

func initializeEncryptionWorkers(wg *sync.WaitGroup, fileChannel chan FileData, s3Context *S3Context) {
	for range 5 {
		wg.Add(1)
		go EncryptFile(fileChannel, s3Context)
	}
}

func declareRoutes(router *mux.Router, fileChannel chan FileData, encryptedFileChannel chan EncryptedFileData,
	s3Context *S3Context, plainDataRepository map[string]FileData) {

	router.HandleFunc("/encrypt", func(writer http.ResponseWriter, request *http.Request) {
		HandlePlainFileUpload(writer, request, fileChannel)
	})

	router.HandleFunc("/decrypt", func(writer http.ResponseWriter, request *http.Request) {
		HandleEncryptedFileUpload(writer, request, encryptedFileChannel)
	})

	router.HandleFunc("/download/encrypted/{uuid}", func(writer http.ResponseWriter, request *http.Request) {
		uuidFromRequest := mux.Vars(request)["uuid"]
		HandleEncryptedDownload(writer, request, s3Context, uuidFromRequest)
	})

	router.HandleFunc("/download/decrypted/{uuid}", func(writer http.ResponseWriter, request *http.Request) {
		uuidFromRequest := mux.Vars(request)["uuid"]
		HandlePlainDownload(writer, request, plainDataRepository, uuidFromRequest)
	})
}
