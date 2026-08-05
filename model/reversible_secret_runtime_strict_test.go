package model

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type legacyRuntimeSecretFixtures struct {
	channelID         int
	channelSecret     string
	twoFAUserID       int
	twoFASecret       string
	optionKey         string
	optionSecret      string
	customOAuthID     int
	customOAuthSecret string
	userID            int
	userWebhookSecret string
	userGotifyToken   string
}

func insertLegacyRuntimeSecretFixtures(
	t *testing.T,
) legacyRuntimeSecretFixtures {
	t.Helper()
	fixtures := legacyRuntimeSecretFixtures{
		channelID:         8101,
		channelSecret:     "runtime-legacy-channel-secret",
		twoFAUserID:       8102,
		twoFASecret:       "runtime-legacy-twofa-secret",
		optionKey:         "SMTPToken",
		optionSecret:      "runtime-legacy-option-secret",
		customOAuthID:     8103,
		customOAuthSecret: "runtime-legacy-oauth-secret",
		userID:            8104,
		userWebhookSecret: "runtime-legacy-user-webhook-secret",
		userGotifyToken:   "runtime-legacy-user-gotify-token",
	}

	insertRawChannel(
		t,
		fixtures.channelID,
		"runtime-legacy-channel",
		fixtures.channelSecret,
	)
	insertRawTwoFA(
		t,
		8102,
		fixtures.twoFAUserID,
		fixtures.twoFASecret,
		nil,
	)
	require.NoError(t, DB.Table("options").Create(map[string]any{
		"key":   fixtures.optionKey,
		"value": fixtures.optionSecret,
	}).Error)
	insertRawCustomOAuthProvider(
		t,
		fixtures.customOAuthID,
		"runtime-legacy-oauth",
		fixtures.customOAuthSecret,
	)
	insertRawUserSetting(
		t,
		fixtures.userID,
		"runtime-legacy-user-setting",
		marshalUserSetting(t, dto.UserSetting{
			WebhookSecret: fixtures.userWebhookSecret,
			GotifyToken:   fixtures.userGotifyToken,
		}),
		nil,
	)

	return fixtures
}

func runtimeSecretReaders(
	fixtures legacyRuntimeSecretFixtures,
) []struct {
	name    string
	secrets []string
	read    func() (string, error)
} {
	return []struct {
		name    string
		secrets []string
		read    func() (string, error)
	}{
		{
			name:    "channel",
			secrets: []string{fixtures.channelSecret},
			read: func() (string, error) {
				channel, err := GetChannelById(fixtures.channelID, true)
				if err != nil {
					return "", err
				}
				return channel.Key, nil
			},
		},
		{
			name:    "two factor",
			secrets: []string{fixtures.twoFASecret},
			read: func() (string, error) {
				factor, err := GetTwoFAByUserId(fixtures.twoFAUserID)
				if err != nil {
					return "", err
				}
				return factor.Secret, nil
			},
		},
		{
			name:    "protected option",
			secrets: []string{fixtures.optionSecret},
			read: func() (string, error) {
				options, err := AllOption()
				if err != nil {
					return "", err
				}
				if len(options) != 1 {
					return "", assert.AnError
				}
				return options[0].Value, nil
			},
		},
		{
			name:    "custom OAuth",
			secrets: []string{fixtures.customOAuthSecret},
			read: func() (string, error) {
				provider, err := GetCustomOAuthProviderById(fixtures.customOAuthID)
				if err != nil {
					return "", err
				}
				return provider.ClientSecret, nil
			},
		},
		{
			name: "credential-bearing user setting",
			secrets: []string{
				fixtures.userWebhookSecret,
				fixtures.userGotifyToken,
			},
			read: func() (string, error) {
				user, err := GetUserById(fixtures.userID, true)
				if err != nil {
					return "", err
				}
				return user.Setting, nil
			},
		},
	}
}

func TestRuntimeReadersRejectReintroducedProtectedPlaintext(t *testing.T) {
	prepareUserSettingSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	fixtures := insertLegacyRuntimeSecretFixtures(t)

	for _, test := range runtimeSecretReaders(fixtures) {
		t.Run(test.name, func(t *testing.T) {
			plaintext, err := test.read()

			require.Error(t, err)
			assert.Empty(t, plaintext)
			assert.Contains(t, err.Error(), "legacy plaintext")
			for _, secret := range test.secrets {
				assert.NotContains(t, err.Error(), secret)
			}
		})
	}
}

func TestPreparationModeRuntimeReadersAcceptLegacyProtectedValues(t *testing.T) {
	prepareUserSettingSecretTest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"false",
	)
	fixtures := insertLegacyRuntimeSecretFixtures(t)

	for _, test := range runtimeSecretReaders(fixtures) {
		t.Run(test.name, func(t *testing.T) {
			plaintext, err := test.read()

			require.NoError(t, err)
			for _, secret := range test.secrets {
				assert.Contains(t, plaintext, secret)
			}
		})
	}
}
