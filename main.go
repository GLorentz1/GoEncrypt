package main

import (
	"GoEncryptApi/aws"
	"GoEncryptApi/encryption"
	"GoEncryptApi/handlers"
	"GoEncryptApi/types"
	"github.com/gorilla/mux"
	"log"
	"net/http"
)

func main() {
	router := mux.NewRouter()
	s3Context := aws.InitializeS3Client()
	fileChannel := make(chan types.FileData, 10)
	encryptedFileChannel := make(chan types.FileData, 10)

	declareRoutes(router, fileChannel, &s3Context)
	go func() {
		errListenAndServe := http.ListenAndServe("localhost:80", router)
		if errListenAndServe != nil {
			panic(errListenAndServe)
		}
	}()

	for {
		select {
		case fileData := <-fileChannel:
			func() {
				log.Printf("Calling encryption go routine")
				go encryption.Encrypt(fileData, encryptedFileChannel)
			}()
		case encryptedFileData := <-encryptedFileChannel:
			func() {
				log.Printf("Calling upload go routine")
				go aws.UploadPart(&s3Context, encryptedFileData)
			}()
		}
	}
}

func declareRoutes(router *mux.Router, fileChannel chan types.FileData, s3Context *aws.S3Context) {

	router.PathPrefix("/public/").Handler(http.StripPrefix("/public/", http.FileServer(http.Dir("public"))))

	router.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		handlers.HandleHome(writer, request)
	})

	router.HandleFunc("/encrypt", func(writer http.ResponseWriter, request *http.Request) {
		handlers.HandleEncryption(writer, request, fileChannel)
	})

	router.HandleFunc("/download/encrypted/{uuid}", func(writer http.ResponseWriter, request *http.Request) {
		uuidFromRequest := mux.Vars(request)["uuid"]
		handlers.HandleEncryptedDownload(writer, request, s3Context, uuidFromRequest)
	})

	router.HandleFunc("/decrypt", func(writer http.ResponseWriter, request *http.Request) {
		handlers.HandleDecryption(writer, request, s3Context)
	})
}
