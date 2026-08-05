package middleware

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/constant"
	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
)

type readCloser struct {
	io.Reader
	closeFn func() error
}

func (rc *readCloser) Close() error {
	if rc.closeFn != nil {
		return rc.closeFn()
	}
	return nil
}

// Decompression-bomb guard: a tiny compressed body must not be able to
// force the server to inflate the full size cap. Legitimate JSON rarely
// compresses beyond ~100:1, gzip bombs approach ~1000:1, so cap the
// expansion ratio once output exceeds a grace floor that every normal
// small request stays under.
const (
	decompressionRatioLimit      = 300
	decompressionRatioGraceBytes = 1 << 20 // 1 MiB
)

var errDecompressionBomb = errors.New("compressed request body exceeds allowed expansion ratio")

type countingReader struct {
	r io.Reader
	n int64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	cr.n += int64(n)
	return n, err
}

// ratioLimitedReader reads decompressed data while tracking how much
// compressed input has been consumed, and fails once the expansion
// ratio becomes implausible for legitimate traffic.
type ratioLimitedReader struct {
	decompressed io.Reader
	compressed   *countingReader
	out          int64
}

func (rr *ratioLimitedReader) Read(p []byte) (int, error) {
	n, err := rr.decompressed.Read(p)
	rr.out += int64(n)
	if rr.out > decompressionRatioGraceBytes {
		in := rr.compressed.n
		if in > 0 && rr.out/in > decompressionRatioLimit {
			return n, errDecompressionBomb
		}
	}
	return n, err
}

func DecompressRequestMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body == nil || c.Request.Method == http.MethodGet {
			c.Next()
			return
		}
		maxMB := constant.MaxRequestBodyMB
		if maxMB <= 0 {
			maxMB = 32
		}
		maxBytes := int64(maxMB) << 20

		origBody := c.Request.Body
		wrapMaxBytes := func(body io.ReadCloser) io.ReadCloser {
			return http.MaxBytesReader(c.Writer, body, maxBytes)
		}

		switch c.GetHeader("Content-Encoding") {
		case "gzip":
			compressedCounter := &countingReader{r: origBody}
			gzipReader, err := gzip.NewReader(compressedCounter)
			if err != nil {
				_ = origBody.Close()
				c.AbortWithStatus(http.StatusBadRequest)
				return
			}
			// Replace the request body with the decompressed data, and enforce a max size (post-decompression)
			// plus an expansion-ratio bound (anti decompression bomb).
			c.Request.Body = wrapMaxBytes(&readCloser{
				Reader: &ratioLimitedReader{decompressed: gzipReader, compressed: compressedCounter},
				closeFn: func() error {
					_ = gzipReader.Close()
					return origBody.Close()
				},
			})
			c.Request.Header.Del("Content-Encoding")
		case "br":
			compressedCounter := &countingReader{r: origBody}
			reader := brotli.NewReader(compressedCounter)
			c.Request.Body = wrapMaxBytes(&readCloser{
				Reader: &ratioLimitedReader{decompressed: reader, compressed: compressedCounter},
				closeFn: func() error {
					return origBody.Close()
				},
			})
			c.Request.Header.Del("Content-Encoding")
		default:
			// Even for uncompressed bodies, enforce a max size to avoid huge request allocations.
			c.Request.Body = wrapMaxBytes(origBody)
		}

		// Continue processing the request
		c.Next()
	}
}
