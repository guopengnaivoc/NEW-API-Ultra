package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetUpstreamMetadataCaches(t *testing.T) {
	t.Helper()
	cacheMutex.Lock()
	defer cacheMutex.Unlock()
	etagCache = make(map[string]string)
	bodyCache = make(map[string][]byte)
}

func TestUpstreamMetadataDigestPinsRejectMalformedEntries(t *testing.T) {
	sum := sha256.Sum256([]byte("upstream metadata pin fixture"))
	digest := hex.EncodeToString(sum[:])
	tests := []struct {
		name    string
		env     string
		want    map[string]string
		wantErr bool
	}{
		{name: "unset", env: "", want: nil},
		{
			name: "single pin",
			env:  "https://example.test/models.json=" + digest,
			want: map[string]string{"https://example.test/models.json": digest},
		},
		{
			name: "comma and whitespace separated",
			env:  " https://a.test/a.json=" + digest + " ,\nhttps://b.test/b.json=" + digest,
			want: map[string]string{
				"https://a.test/a.json": digest,
				"https://b.test/b.json": digest,
			},
		},
		{
			// Digests are commonly pasted from tooling that emits uppercase.
			name: "uppercase digest is normalized",
			env:  "https://a.test/a.json=" + strings.ToUpper(digest),
			want: map[string]string{"https://a.test/a.json": digest},
		},
		// A mistyped pin must not silently degrade to the transport-only
		// check the operator was trying to escape.
		{name: "missing separator", env: "https://a.test/a.json", wantErr: true},
		{name: "missing url", env: "=" + digest, wantErr: true},
		{name: "digest too short", env: "https://a.test/a.json=abcdef", wantErr: true},
		{name: "digest not hex", env: "https://a.test/a.json=" + digest[:62] + "zz", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(upstreamMetadataDigestEnv, test.env)
			pins, err := upstreamMetadataDigestPins()
			if test.wantErr {
				require.Error(t, err)
				assert.Nil(t, pins)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, pins)
		})
	}
}

func TestCheckUpstreamMetadataSourceRequiresHTTPSUnlessPinned(t *testing.T) {
	digest := hex.EncodeToString(make([]byte, sha256.Size))
	tests := []struct {
		name    string
		env     string
		url     string
		wantErr bool
	}{
		{name: "https accepted", url: "https://example.test/models.json"},
		{name: "plaintext rejected", url: "http://example.test/models.json", wantErr: true},
		{name: "non-http scheme rejected", url: "file:///etc/models.json", wantErr: true},
		{name: "scheme-less rejected", url: "example.test/models.json", wantErr: true},
		{
			// A pinned digest is a stronger guarantee than TLS, so it stands
			// in for the transport requirement (mirrors, air-gapped hosts).
			name: "pinned plaintext accepted",
			env:  "http://example.test/models.json=" + digest,
			url:  "http://example.test/models.json",
		},
		{
			name:    "pin for a different url does not help",
			env:     "http://other.test/models.json=" + digest,
			url:     "http://example.test/models.json",
			wantErr: true,
		},
		{name: "malformed pin fails closed", env: "garbage", url: "https://example.test/models.json", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(upstreamMetadataDigestEnv, test.env)
			err := checkUpstreamMetadataSource(test.url)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestCheckUpstreamMetadataBodyEnforcesPinnedDigest(t *testing.T) {
	body := []byte(`{"success":true,"data":[]}`)
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	const target = "https://example.test/models.json"

	tests := []struct {
		name    string
		env     string
		body    []byte
		wantErr bool
	}{
		{name: "unpinned source is not verified", body: body},
		{name: "matching digest accepted", env: target + "=" + digest, body: body},
		{name: "single byte change rejected", env: target + "=" + digest, body: append(body[:len(body)-1:len(body)-1], ' ', '}'), wantErr: true},
		{name: "empty body rejected", env: target + "=" + digest, body: nil, wantErr: true},
		{name: "malformed pin fails closed", env: "garbage", body: body, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(upstreamMetadataDigestEnv, test.env)
			err := checkUpstreamMetadataBody(target, test.body)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestIsSupportedUpstreamScheme(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{url: "https://example.test", want: true},
		{url: "http://192.168.0.10:3000", want: true},
		// The previous strings.HasPrefix(url, "http") test admitted all of
		// these, which then flowed into http.NewRequest as an upstream.
		{url: "httpx://example.test", want: false},
		{url: "https-example.test", want: false},
		{url: "file:///etc/passwd", want: false},
		{url: "https://", want: false},
		{url: "", want: false},
	}
	for _, test := range tests {
		t.Run(test.url, func(t *testing.T) {
			assert.Equal(t, test.want, isSupportedUpstreamScheme(test.url))
		})
	}
}

func TestIsShippedMetadataPresetHost(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{url: officialRatioPresetBaseURL + "/api/pricing", want: true},
		{url: modelsDevPresetBaseURL + modelsDevPath, want: true},
		{url: "https://BaseLLM.GitHub.io/api/pricing", want: true},
		// An operator's own channel keeps the looser rule: plaintext inside a
		// private network is a deployment choice, not a supply-chain risk.
		{url: "http://192.168.0.10:3000/api/pricing", want: false},
		{url: "https://basellm.github.io.evil.test/api/pricing", want: false},
	}
	for _, test := range tests {
		t.Run(test.url, func(t *testing.T) {
			assert.Equal(t, test.want, isShippedMetadataPresetHost(test.url))
		})
	}
}

func TestFetchJSONRejectsTamperedBodyWithoutPoisoningCache(t *testing.T) {
	resetUpstreamMetadataCaches(t)
	t.Cleanup(func() { resetUpstreamMetadataCaches(t) })

	served := []byte(`{"success":true,"data":[{"model_name":"gpt-tampered"}]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(served)
	}))
	t.Cleanup(server.Close)

	expected := sha256.Sum256([]byte(`{"success":true,"data":[{"model_name":"gpt-reviewed"}]}`))
	t.Setenv(upstreamMetadataDigestEnv, server.URL+"="+hex.EncodeToString(expected[:]))
	t.Setenv("SYNC_HTTP_RETRY", "1")

	var out upstreamEnvelope[upstreamModel]
	require.Error(t, fetchJSON(context.Background(), server.URL, &out))
	assert.Empty(t, out.Data)

	// A rejected document must not be retained: a later conditional request
	// answered with 304 would otherwise replay it as if it had been verified.
	cacheMutex.RLock()
	defer cacheMutex.RUnlock()
	assert.Empty(t, bodyCache[server.URL])
	assert.Empty(t, etagCache[server.URL])
}

func TestFetchJSONAcceptsPinnedBodyOverPlaintext(t *testing.T) {
	resetUpstreamMetadataCaches(t)
	t.Cleanup(func() { resetUpstreamMetadataCaches(t) })

	served := []byte(`{"success":true,"data":[{"model_name":"gpt-reviewed"}]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(served)
	}))
	t.Cleanup(server.Close)

	sum := sha256.Sum256(served)
	t.Setenv(upstreamMetadataDigestEnv, server.URL+"="+hex.EncodeToString(sum[:]))
	t.Setenv("SYNC_HTTP_RETRY", "1")

	var out upstreamEnvelope[upstreamModel]
	require.NoError(t, fetchJSON(context.Background(), server.URL, &out))
	require.Len(t, out.Data, 1)
	assert.Equal(t, "gpt-reviewed", out.Data[0].ModelName)
}

func TestFetchJSONRejectsPlaintextSourceBeforeDialing(t *testing.T) {
	resetUpstreamMetadataCaches(t)
	t.Cleanup(func() { resetUpstreamMetadataCaches(t) })

	reached := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
	}))
	t.Cleanup(server.Close)

	t.Setenv(upstreamMetadataDigestEnv, "")

	var out upstreamEnvelope[upstreamModel]
	require.Error(t, fetchJSON(context.Background(), server.URL, &out))
	assert.False(t, reached, "an unusable source must fail before any request is issued")
}
