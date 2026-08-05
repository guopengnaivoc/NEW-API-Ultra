package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func doDecompressedRequest(t *testing.T, body []byte) (int, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(DecompressRequestMiddleware())

	var readErr error
	var readBytes int
	router.POST("/echo", func(c *gin.Context) {
		n, err := io.Copy(io.Discard, c.Request.Body)
		readBytes = int(n)
		readErr = err
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(body))
	req.Header.Set("Content-Encoding", "gzip")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	_ = readBytes
	return recorder.Code, readErr
}

func gzipCompress(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	_, err := writer.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buf.Bytes()
}

// A tiny gzip body expanding to tens of MiB must fail with the ratio
// guard long before the 32 MiB size cap is reached (NA-ISSUE-0112).
func TestDecompressRejectsGzipBomb(t *testing.T) {
	bomb := gzipCompress(t, bytes.Repeat([]byte{0}, 24<<20)) // ~24 KiB compressed -> 24 MiB
	status, readErr := doDecompressedRequest(t, bomb)
	require.Equal(t, http.StatusOK, status)
	require.ErrorIs(t, readErr, errDecompressionBomb)
}

// Normal compressed JSON-like payloads must decompress fully.
func TestDecompressAllowsNormalPayload(t *testing.T) {
	payload := bytes.Repeat([]byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hello world"}]}`), 20000) // ~1.4 MiB
	status, readErr := doDecompressedRequest(t, gzipCompress(t, payload))
	require.Equal(t, http.StatusOK, status)
	require.NoError(t, readErr)
}
