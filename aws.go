package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"log"
	"sort"
	"sync"
)

type UploadInfo struct {
	UploadID string
	Parts    []types.CompletedPart
	wg       sync.WaitGroup
}

type S3Context struct {
	service              *s3.Client
	fileUploadRepository map[string]*UploadInfo
	mu                   sync.Mutex
}

func InitializeS3Client() S3Context {
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion("us-east-1"))
	if err != nil {
		log.Fatalf("unable to load SDK config, %v", err)
	}

	svc := s3.NewFromConfig(cfg)

	return S3Context{service: svc, fileUploadRepository: make(map[string]*UploadInfo)}
}

func UploadPart(s3Context *S3Context, data EncryptedFileData) {
	s3Key := data.fileUUID.String()

	s3Context.mu.Lock()
	info, exists := s3Context.fileUploadRepository[s3Key]

	if !exists {
		uploadId, err := startMultipartUpload(s3Context.service, s3Key)

		if err != nil {
			log.Printf("failed to start multipart upload for file %s: %v", s3Key, err)
			s3Context.mu.Unlock()
			return
		}

		info = &UploadInfo{
			UploadID: uploadId,
			Parts:    make([]types.CompletedPart, 0),
		}

		s3Context.fileUploadRepository[data.fileUUID.String()] = info
		fmt.Printf("Starting multipart upload with id %s\n", info.UploadID)
	} else {
		fmt.Printf("Continuing multipart upload with id %s\n", info.UploadID)
	}
	s3Context.mu.Unlock()

	info.wg.Add(1)
	go func() {
		_ = uploadChunk(s3Context, s3Key, s3Context.fileUploadRepository[data.fileUUID.String()], data.bytes, data.counter)

		if data.isLastChunk {
			info.wg.Wait()
			err := completeMultipartUpload(s3Context.service, s3Key, s3Context.fileUploadRepository[data.fileUUID.String()])
			if err != nil {
				log.Printf("failed to complete multipart upload for file %s", s3Key)
				log.Printf("%v", err)
			} else {
				s3Context.mu.Lock()
				delete(s3Context.fileUploadRepository, s3Key)
				s3Context.mu.Unlock()
				log.Printf("Successfully finished multipart upload for file %s", s3Key)
			}
		}
	}()
}

func startMultipartUpload(client *s3.Client, key string) (string, error) {
	ctx := context.TODO()
	createOutput, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String("goencrypt"),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", fmt.Errorf("failed to create multipart upload: %v\n", err)
	}
	return *createOutput.UploadId, nil
}

func uploadChunk(s3Context *S3Context, key string, info *UploadInfo, data []byte, part int32) error {
	ctx := context.TODO()

	fmt.Printf("Uploading chunk with partNumber %d and upload id %s\n", part, info.UploadID)

	uploadPartOutput, err := s3Context.service.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String("goencrypt"),
		Key:        aws.String(key),
		PartNumber: &part,
		UploadId:   aws.String(info.UploadID),
		Body:       bytes.NewReader(data),
	})
	if err != nil {
		return fmt.Errorf("failed to upload part: %v", err)
	}

	s3Context.mu.Lock()
	info.Parts = append(info.Parts, types.CompletedPart{
		ETag:       uploadPartOutput.ETag,
		PartNumber: &part,
	})
	s3Context.mu.Unlock()

	info.wg.Done()
	return nil
}

func completeMultipartUpload(client *s3.Client, key string, info *UploadInfo) error {
	ctx := context.TODO()

	fmt.Printf("Completing multipart upload for key %s and upload id %s\n", key, info.UploadID)

	sort.Slice(info.Parts, func(i, j int) bool {
		return *info.Parts[i].PartNumber < *info.Parts[j].PartNumber
	})

	_, err := client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String("goencrypt"),
		Key:      aws.String(key),
		UploadId: aws.String(info.UploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: info.Parts,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to complete multipart upload: %v", err)
	}
	return nil
}
