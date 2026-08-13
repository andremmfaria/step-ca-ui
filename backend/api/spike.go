package api

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
)

// spikeBlobBody is the placeholder payload for GET /api/v1/_spike/blob,
// unrelated to any certificate: this operation exists only to prove the
// []byte-response mechanic Phase 4 reuses for real downloads (5.7).
var spikeBlobBody = []byte("step-ca-ui phase 0 spike blob\n")

type spikeBlobOutput struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	Body               []byte
}

// spikeBlobResponses is hand-written because huma does not derive an
// application/octet-stream + format:binary response from a []byte body
// field on its own (item 7, 5.7).
var spikeBlobResponses = map[string]*huma.Response{
	"200": {
		Description: "Placeholder binary payload.",
		Content: map[string]*huma.MediaType{
			"application/octet-stream": {
				Schema: &huma.Schema{Type: "string", Format: "binary"},
			},
		},
	},
}

func getSpikeBlob(_ context.Context, _ *struct{}) (*spikeBlobOutput, error) {
	return &spikeBlobOutput{
		ContentType:        "application/octet-stream",
		ContentDisposition: `attachment; filename="spike.bin"`,
		Body:               spikeBlobBody,
	}, nil
}

type spikeUploadInput struct {
	RawBody huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" contentType:"application/octet-stream" required:"true"`
	}]
}

type spikeUploadBody struct {
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"sizeBytes"`
}

type spikeUploadOutput struct {
	Body spikeUploadBody
}

// postSpikeUpload proves huma.MultipartFormFiles against
// humachi.MultipartMaxMemory=1MiB (item 7, 5.7); FormFile.Size and
// .Filename come from the parsed multipart header, no file content is read.
func postSpikeUpload(_ context.Context, in *spikeUploadInput) (*spikeUploadOutput, error) {
	f := in.RawBody.Data().File
	return &spikeUploadOutput{Body: spikeUploadBody{Filename: f.Filename, SizeBytes: f.Size}}, nil
}
