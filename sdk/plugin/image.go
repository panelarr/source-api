// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"errors"
	"io"

	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
)

// ImageChunkSize is how many bytes [SendImage] puts in one message.
const ImageChunkSize = 64 << 10

// SendImage streams an image to the host: the metadata first, then the
// bytes of r in chunks. size is the total length, or 0 when unknown.
func SendImage(send ImageSendFunc, contentType string, size int64, r io.Reader) error {
	meta := &sourcev1.ImageMetadata{ContentType: contentType}
	if size > 0 {
		meta.ContentLength = uint64(size)
	}
	if err := send(&sourcev1.FetchImageResponse{Part: &sourcev1.FetchImageResponse_Metadata{Metadata: meta}}); err != nil {
		return err
	}
	buf := make([]byte, ImageChunkSize)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if serr := send(&sourcev1.FetchImageResponse{Part: &sourcev1.FetchImageResponse_Chunk{Chunk: chunk}}); serr != nil {
				return serr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return ErrSourceDown("reading the image: %v", err)
		}
	}
}
