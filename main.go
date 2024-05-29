package main

import (
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"log"
	"net/http"
)

type FileData struct {
	filename    string
	password    string
	fileUUID    uuid.UUID
	bytes       []byte
	counter     int32
	isLastChunk bool
}

func main() {
	router := mux.NewRouter()
	s3Context := InitializeS3Client()
	fileChannel := make(chan FileData, 10)
	encryptedFileChannel := make(chan FileData, 10)

	declareRoutes(router, fileChannel, &s3Context)
	go func() {
		errListenAndServe := http.ListenAndServe(":5678", router)
		if errListenAndServe != nil {
			panic(errListenAndServe)
		}
	}()

	for {
		select {
		case fileData := <-fileChannel:
			func() {
				log.Printf("Calling encryption go routine")
				go Encrypt(fileData, encryptedFileChannel)
			}()
		case encryptedFileData := <-encryptedFileChannel:
			func() {
				log.Printf("Calling upload go routine")
				go UploadPart(&s3Context, encryptedFileData)
			}()
		}
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
