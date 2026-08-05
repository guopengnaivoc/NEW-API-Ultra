package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func oversizedResponseClient(body *trackingResponseBody, contentLength int64) *http.Client {
	return &http.Client{
		Transport: responseRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Body:          body,
				ContentLength: contentLength,
				Header:        make(http.Header),
				Request:       req,
			}, nil
		}),
	}
}

func TestFetchLatestCodexClientVersionRejectsOversizedResponse(t *testing.T) {
	body := newTrackingResponseBody(`{"name":"v1.2.3"}`)
	client := oversizedResponseClient(body, codexMetadataResponseMaxBytes+1)

	version, err := fetchLatestCodexClientVersion(
		context.Background(),
		client,
		"https://api.example/releases/latest",
	)

	require.ErrorIs(t, err, errServiceResponseTooLarge)
	assert.Empty(t, version)
	assert.True(t, body.closed)
	assert.Zero(t, body.reads)
}

func TestFetchCodexModelsRejectsOversizedResponse(t *testing.T) {
	body := newTrackingResponseBody(`{"models":[{"slug":"gpt-test"}]}`)
	client := oversizedResponseClient(body, codexMetadataResponseMaxBytes+1)

	statusCode, models, err := FetchCodexModels(
		context.Background(),
		client,
		"https://api.example",
		&CodexOAuthKey{AccessToken: "access", AccountID: "account"},
		"1.2.3",
	)

	require.ErrorIs(t, err, errServiceResponseTooLarge)
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Nil(t, models)
	assert.True(t, body.closed)
	assert.Zero(t, body.reads)
}

func TestRefreshCodexOAuthTokenRejectsOversizedResponse(t *testing.T) {
	body := newTrackingResponseBody(
		`{"access_token":"access","refresh_token":"refresh","expires_in":3600}`,
	)
	client := oversizedResponseClient(body, codexOAuthResponseMaxBytes+1)

	result, err := refreshCodexOAuthToken(
		context.Background(),
		client,
		"https://auth.example/oauth/token",
		"client-id",
		"refresh-token",
	)

	require.ErrorIs(t, err, errServiceResponseTooLarge)
	assert.Nil(t, result)
	assert.True(t, body.closed)
	assert.Zero(t, body.reads)
}

func TestCodexWhamOperationsRejectOversizedResponses(t *testing.T) {
	testCases := []struct {
		name string
		call func(context.Context, *http.Client) (int, []byte, error)
	}{
		{
			name: "usage",
			call: func(ctx context.Context, client *http.Client) (int, []byte, error) {
				return FetchCodexWhamUsage(ctx, client, "https://api.example", "access", "account")
			},
		},
		{
			name: "reset credits",
			call: func(ctx context.Context, client *http.Client) (int, []byte, error) {
				return FetchCodexWhamRateLimitResetCredits(ctx, client, "https://api.example", "access", "account")
			},
		},
		{
			name: "consume reset credit",
			call: func(ctx context.Context, client *http.Client) (int, []byte, error) {
				return ConsumeCodexWhamRateLimitResetCredit(ctx, client, "https://api.example", "access", "account")
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			body := newTrackingResponseBody(`{"ok":true}`)
			client := oversizedResponseClient(body, codexMetadataResponseMaxBytes+1)

			statusCode, responseBody, err := testCase.call(context.Background(), client)

			require.ErrorIs(t, err, errServiceResponseTooLarge)
			assert.Equal(t, http.StatusOK, statusCode)
			assert.Nil(t, responseBody)
			assert.True(t, body.closed)
			assert.Zero(t, body.reads)
		})
	}
}
