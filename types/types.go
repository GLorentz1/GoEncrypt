package types

import "github.com/google/uuid"

type FileData struct {
	Filename    string
	Password    string
	FileUUID    uuid.UUID
	Bytes       []byte
	Counter     int32
	IsLastChunk bool
}

type Response struct {
	Status int
	Msg    string
}
