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

	declareRoutes(router, fileChannel, &s3Context)
	initializeEncryptionWorkers(&wg, fileChannel, &s3Context)

	errListenAndServe := http.ListenAndServe(":5678", router)
	if errListenAndServe != nil {
		panic(errListenAndServe)
	}

	wg.Wait()
}

func initializeEncryptionWorkers(wg *sync.WaitGroup, fileChannel chan FileData, s3Context *S3Context) {
	for range 5 {
		wg.Add(1)
		go Encrypt(fileChannel, s3Context)
	}
}

func declareRoutes(router *mux.Router, fileChannel chan FileData, s3Context *S3Context) {

	router.HandleFunc("/encrypt", func(writer http.ResponseWriter, request *http.Request) {
		HandlePlainFileUpload(writer, request, fileChannel)
	})

	router.HandleFunc("/download/encrypted/{uuid}", func(writer http.ResponseWriter, request *http.Request) {
		uuidFromRequest := mux.Vars(request)["uuid"]
		HandleEncryptedDownload(writer, request, s3Context, uuidFromRequest)
	})

	router.HandleFunc("/download/decrypted/{uuid}", func(writer http.ResponseWriter, request *http.Request) {
		uuidFromRequest := mux.Vars(request)["uuid"]
		HandlePlainDownload(writer, request, s3Context, uuidFromRequest)
	})
}
