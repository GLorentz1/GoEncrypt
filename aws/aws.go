package aws

import (
	myTypes "GoEncryptApi/types"
	"bytes"
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"io"
	"log"
	"sort"
	"sync"
	"time"
)

type UploadInfo struct {
	UploadID string
	Parts    []types.CompletedPart
	wg       sync.WaitGroup
}

type S3Context struct {
	Client               *s3.Client
	PresignClient        *Presigner
	FileUploadRepository map[string]*UploadInfo
	mu                   sync.Mutex
}

type Presigner struct {
	PresignClient *s3.PresignClient
}

func InitializeS3Client() S3Context {
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion("us-east-1"))
	if err != nil {
		log.Fatalf("unable to load SDK config, %v", err)
	}

	svc := s3.NewFromConfig(cfg)

	return S3Context{Client: svc, PresignClient: &Presigner{s3.NewPresignClient(svc)},
		FileUploadRepository: make(map[string]*UploadInfo)}
}

func UploadPart(s3Context *S3Context, data myTypes.FileData) {
	s3Key := data.FileUUID.String()

	s3Context.mu.Lock()
	info, exists := s3Context.FileUploadRepository[s3Key]

	if !exists {
		uploadId, err := startMultipartUpload(s3Context.Client, s3Key)

		if err != nil {
			log.Printf("failed to start multipart upload for file %s: %v", s3Key, err)
			s3Context.mu.Unlock()
			return
		}

		info = &UploadInfo{
			UploadID: uploadId,
			Parts:    make([]types.CompletedPart, 0),
		}

		s3Context.FileUploadRepository[data.FileUUID.String()] = info
		fmt.Printf("Starting multipart upload with id %s\n", info.UploadID)
	} else {
		fmt.Printf("Continuing multipart upload with id %s\n", info.UploadID)
	}
	s3Context.mu.Unlock()

	info.wg.Add(1)
	go func() {
		_ = uploadChunk(s3Context, s3Key, s3Context.FileUploadRepository[data.FileUUID.String()], data.Bytes, data.Counter)

		if data.IsLastChunk {
			info.wg.Wait()
			err := completeMultipartUpload(s3Context.Client, s3Key, s3Context.FileUploadRepository[data.FileUUID.String()])
			if err != nil {
				log.Printf("failed to complete multipart upload for file %s", s3Key)
				log.Printf("%v", err)
			} else {
				s3Context.mu.Lock()
				delete(s3Context.FileUploadRepository, s3Key)
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

	fmt.Printf("Uploading chunk with partNumber %d and {%d} bytes\n", part, len(data))

	uploadPartOutput, err := s3Context.Client.UploadPart(ctx, &s3.UploadPartInput{
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

	for _, part := range info.Parts {
		log.Printf("This is a part with number %d", *part.PartNumber)
	}

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

func (presigner Presigner) presignedGetRequest(id string) (*v4.PresignedHTTPRequest, error) {
	request, err := presigner.PresignClient.PresignGetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String("goencrypt"),
		Key:    aws.String(id),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = time.Duration(3600 * int64(time.Second))
	})
	if err != nil {
		log.Printf("Couldn't get a presigned request to get %v:%v. Here's why: %v\n", "goencrypt", id, err)
	}
	return request, err
}

func GetPresignedUrl(s3Context *S3Context, id string) string {
	request, err := s3Context.PresignClient.presignedGetRequest(id)

	if err != nil {
		log.Printf("Failed to get presigned url for id %d", id)
	}

	return request.URL
}

func HeadFile(client *s3.Client, key string) (int64, error) {
	log.Printf("Heading file %s", key)

	object, err := client.HeadObject(context.TODO(),
		&s3.HeadObjectInput{Bucket: aws.String("goencrypt"), Key: aws.String(key)})

	if err != nil {
		log.Printf("Couldn't head file %s, error: %v", key, err)
	}

	if object != nil {
		return *object.ContentLength, nil
	}

	return -1, err
}

func DownloadChunk(client *s3.Client, key string, start, end int64) ([]byte, error) {
	log.Printf("Received a DownloadChunk request with start={%d} and end={%d}", start, end)
	rangeHeader := fmt.Sprintf("bytes=%d-%d", start, end)

	output, err := client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String("goencrypt"),
		Key:    aws.String(key),
		Range:  aws.String(rangeHeader),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to download chunk: %w", err)
	}
	defer output.Body.Close()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, output.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read chunk: %w", err)
	}

	log.Printf("Returning a buffer with %d bytes", len(buf.Bytes()))

	return buf.Bytes(), nil
}
