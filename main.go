package main

import (
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"net/http"
	"sync"
)

type FileData struct {
	filename string
	password string
	fileUUID uuid.UUID
	bytes    []byte
}

type EncryptedFileData struct {
	filename string
	password string
	fileUUID uuid.UUID
	bytes    []byte
}

func main() {
	var wg sync.WaitGroup
	router := mux.NewRouter()

	fileChannel := make(chan FileData, 10)
	encryptedFileChannel := make(chan EncryptedFileData, 10)

	encryptedDataRepository := make(map[string]EncryptedFileData, 10)
	plainDataRepository := make(map[string]FileData, 10)

	router.HandleFunc("/encrypt", func(writer http.ResponseWriter, request *http.Request) {
		HandlePlainFileUpload(writer, request, fileChannel)
	})

	router.HandleFunc("/decrypt", func(writer http.ResponseWriter, request *http.Request) {
		HandleEncryptedFileUpload(writer, request, encryptedFileChannel)
	})

	router.HandleFunc("/download/encrypted/{uuid}", func(writer http.ResponseWriter, request *http.Request) {
		uuidFromRequest := mux.Vars(request)["uuid"]
		HandleEncryptedDownload(writer, request, encryptedDataRepository, uuidFromRequest)
	})

	router.HandleFunc("/download/decrypted/{uuid}", func(writer http.ResponseWriter, request *http.Request) {
		uuidFromRequest := mux.Vars(request)["uuid"]
		HandlePlainDownload(writer, request, plainDataRepository, uuidFromRequest)
	})

	for range 5 {
		wg.Add(1)
		go EncryptFile(fileChannel, encryptedDataRepository)
	}

	for range 5 {
		wg.Add(1)
		go DecryptFile(encryptedFileChannel, plainDataRepository)
	}

	errListenAndServe := http.ListenAndServe(":5678", router)
	if errListenAndServe != nil {
		panic(errListenAndServe)
	}

	wg.Wait()
}
