package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
)

const (
	dataEncryptionEnvelopePrefix = "naenc:"
	dataEncryptionEnvelopeName   = "naenc"
	dataEncryptionEnvelopeV1     = "v1"
	dataEncryptionKeySize        = 32
	dataEncryptionKeyIDMaxLength = 32
)

type DataEncryptionEnvelopeInfo struct {
	Encrypted bool
	KeyID     string
}

type dataEncryptionConfig struct {
	enabled     bool
	activeKeyID string
	keys        map[string][dataEncryptionKeySize]byte
}

type parsedDataEncryptionEnvelope struct {
	keyID       string
	wrappedKey  []byte
	dataPayload []byte
}

var activeDataEncryptionConfig atomic.Pointer[dataEncryptionConfig]

func currentDataEncryptionConfig() *dataEncryptionConfig {
	config := activeDataEncryptionConfig.Load()
	if config != nil {
		return config
	}
	return &dataEncryptionConfig{enabled: true}
}

func InitDataEncryption() error {
	enabled := true
	if rawEnabled := strings.TrimSpace(os.Getenv("DATA_ENCRYPTION_ENABLE")); rawEnabled != "" {
		parsedEnabled, err := strconv.ParseBool(rawEnabled)
		if err != nil {
			return errors.New("DATA_ENCRYPTION_ENABLE must be true or false")
		}
		enabled = parsedEnabled
	}

	rawKeys := strings.TrimSpace(os.Getenv("DATA_ENCRYPTION_KEYS"))
	activeKeyID := strings.TrimSpace(os.Getenv("DATA_ENCRYPTION_ACTIVE_KEY_ID"))
	if rawKeys == "" {
		if activeKeyID != "" {
			return errors.New("DATA_ENCRYPTION_ACTIVE_KEY_ID requires DATA_ENCRYPTION_KEYS")
		}
		activeDataEncryptionConfig.Store(&dataEncryptionConfig{enabled: enabled})
		return nil
	}
	if !validDataEncryptionKeyID(activeKeyID) {
		return errors.New("DATA_ENCRYPTION_ACTIVE_KEY_ID must be a valid key identifier")
	}

	keys := make(map[string][dataEncryptionKeySize]byte)
	for _, entry := range strings.Split(rawKeys, ",") {
		entry = strings.TrimSpace(entry)
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			return errors.New("DATA_ENCRYPTION_KEYS entries must use key-id=base64-key")
		}
		keyID := strings.TrimSpace(parts[0])
		if !validDataEncryptionKeyID(keyID) {
			return errors.New("DATA_ENCRYPTION_KEYS contains an invalid key identifier")
		}
		if _, exists := keys[keyID]; exists {
			return fmt.Errorf("DATA_ENCRYPTION_KEYS contains duplicate key identifier %q", keyID)
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
		if err != nil {
			return fmt.Errorf("DATA_ENCRYPTION_KEYS entry %q is not valid base64", keyID)
		}
		if len(decoded) != dataEncryptionKeySize {
			return fmt.Errorf("DATA_ENCRYPTION_KEYS entry %q must decode to 32 bytes", keyID)
		}
		var key [dataEncryptionKeySize]byte
		copy(key[:], decoded)
		keys[keyID] = key
	}
	if _, exists := keys[activeKeyID]; !exists {
		return errors.New("DATA_ENCRYPTION_ACTIVE_KEY_ID is not present in DATA_ENCRYPTION_KEYS")
	}

	activeDataEncryptionConfig.Store(&dataEncryptionConfig{
		enabled:     enabled,
		activeKeyID: activeKeyID,
		keys:        keys,
	})
	return nil
}

func DataEncryptionEnabled() bool {
	return currentDataEncryptionConfig().enabled
}

func DataEncryptionConfigured() bool {
	return len(currentDataEncryptionConfig().keys) > 0
}

func IsDataEncryptionEnvelope(value string) bool {
	return strings.HasPrefix(value, dataEncryptionEnvelopePrefix)
}

func SealDataEncryptionValue(domain string, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	config := currentDataEncryptionConfig()
	if !config.enabled {
		return plaintext, nil
	}
	return sealDataEncryptionValue(config, domain, plaintext)
}

// SealDataEncryptionValueRequired encrypts even while the database migration
// gate is disabled. It is used for secondary persistence that must never
// receive a protected plaintext value during a staged rollout.
func SealDataEncryptionValueRequired(domain string, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	return sealDataEncryptionValue(currentDataEncryptionConfig(), domain, plaintext)
}

func sealDataEncryptionValue(
	config *dataEncryptionConfig,
	domain string,
	plaintext string,
) (string, error) {
	if len(config.keys) == 0 || config.activeKeyID == "" {
		return "", fmt.Errorf("data encryption keyring is unavailable for domain %s", domain)
	}

	dataKey := make([]byte, dataEncryptionKeySize)
	if _, err := rand.Read(dataKey); err != nil {
		return "", fmt.Errorf("generate data key for domain %s: %w", domain, err)
	}
	dataPayload, err := sealDataEncryptionBytes(
		dataKey,
		[]byte(plaintext),
		dataEncryptionPayloadAAD(domain),
	)
	if err != nil {
		return "", fmt.Errorf("encrypt value for domain %s: %w", domain, err)
	}

	activeKey := config.keys[config.activeKeyID]
	wrappedKey, err := sealDataEncryptionBytes(
		activeKey[:],
		dataKey,
		dataEncryptionWrapAAD(domain, config.activeKeyID),
	)
	if err != nil {
		return "", fmt.Errorf("wrap data key for domain %s: %w", domain, err)
	}

	return strings.Join([]string{
		dataEncryptionEnvelopeName,
		dataEncryptionEnvelopeV1,
		config.activeKeyID,
		base64.RawURLEncoding.EncodeToString(wrappedKey),
		base64.RawURLEncoding.EncodeToString(dataPayload),
	}, ":"), nil
}

func OpenDataEncryptionValue(
	domain string,
	stored string,
) (string, DataEncryptionEnvelopeInfo, error) {
	return openDataEncryptionValue(domain, stored, false)
}

// OpenLegacyDataEncryptionValueForMigration permits startup migration to
// inspect legacy plaintext before deciding whether the value needs a key.
// Runtime readers must use OpenDataEncryptionValue so plaintext reintroduced
// after migration fails closed.
func OpenLegacyDataEncryptionValueForMigration(
	domain string,
	stored string,
) (string, DataEncryptionEnvelopeInfo, error) {
	return openDataEncryptionValue(domain, stored, true)
}

func openDataEncryptionValue(
	domain string,
	stored string,
	allowLegacyPlaintext bool,
) (string, DataEncryptionEnvelopeInfo, error) {
	if stored == "" {
		return "", DataEncryptionEnvelopeInfo{}, nil
	}
	envelope, encrypted, err := parseDataEncryptionEnvelope(stored)
	if err != nil {
		return "", DataEncryptionEnvelopeInfo{}, fmt.Errorf(
			"invalid encrypted value for domain %s: %w",
			domain,
			err,
		)
	}
	if !encrypted {
		config := currentDataEncryptionConfig()
		if config.enabled && !allowLegacyPlaintext {
			return "", DataEncryptionEnvelopeInfo{}, fmt.Errorf(
				"legacy plaintext is not permitted for domain %s while data encryption enforcement is enabled",
				domain,
			)
		}
		return stored, DataEncryptionEnvelopeInfo{}, nil
	}

	dataKey, err := unwrapDataEncryptionKey(domain, envelope)
	if err != nil {
		return "", DataEncryptionEnvelopeInfo{}, err
	}
	plaintext, err := openDataEncryptionBytes(
		dataKey[:],
		envelope.dataPayload,
		dataEncryptionPayloadAAD(domain),
	)
	if err != nil {
		return "", DataEncryptionEnvelopeInfo{}, fmt.Errorf(
			"authenticate encrypted value for domain %s",
			domain,
		)
	}
	return string(plaintext), DataEncryptionEnvelopeInfo{
		Encrypted: true,
		KeyID:     envelope.keyID,
	}, nil
}

func RewrapDataEncryptionValue(
	domain string,
	stored string,
) (string, bool, error) {
	if stored == "" {
		return "", false, nil
	}
	envelope, encrypted, err := parseDataEncryptionEnvelope(stored)
	if err != nil {
		return "", false, fmt.Errorf("invalid encrypted value for domain %s: %w", domain, err)
	}
	if !encrypted {
		sealed, err := SealDataEncryptionValue(domain, stored)
		if err != nil {
			return "", false, err
		}
		return sealed, sealed != stored, nil
	}

	dataKey, err := unwrapDataEncryptionKey(domain, envelope)
	if err != nil {
		return "", false, err
	}
	if _, err := openDataEncryptionBytes(
		dataKey[:],
		envelope.dataPayload,
		dataEncryptionPayloadAAD(domain),
	); err != nil {
		return "", false, fmt.Errorf("authenticate encrypted value for domain %s", domain)
	}

	config := currentDataEncryptionConfig()
	if !config.enabled || envelope.keyID == config.activeKeyID {
		return stored, false, nil
	}
	activeKey, exists := config.keys[config.activeKeyID]
	if !exists || config.activeKeyID == "" {
		return "", false, fmt.Errorf("active data encryption key is unavailable for domain %s", domain)
	}
	wrappedKey, err := sealDataEncryptionBytes(
		activeKey[:],
		dataKey[:],
		dataEncryptionWrapAAD(domain, config.activeKeyID),
	)
	if err != nil {
		return "", false, fmt.Errorf("rewrap data key for domain %s: %w", domain, err)
	}

	return strings.Join([]string{
		dataEncryptionEnvelopeName,
		dataEncryptionEnvelopeV1,
		config.activeKeyID,
		base64.RawURLEncoding.EncodeToString(wrappedKey),
		base64.RawURLEncoding.EncodeToString(envelope.dataPayload),
	}, ":"), true, nil
}

func parseDataEncryptionEnvelope(
	stored string,
) (parsedDataEncryptionEnvelope, bool, error) {
	if !IsDataEncryptionEnvelope(stored) {
		return parsedDataEncryptionEnvelope{}, false, nil
	}
	parts := strings.Split(stored, ":")
	if len(parts) != 5 ||
		parts[0] != dataEncryptionEnvelopeName ||
		parts[1] != dataEncryptionEnvelopeV1 ||
		!validDataEncryptionKeyID(parts[2]) {
		return parsedDataEncryptionEnvelope{}, true, errors.New("malformed envelope header")
	}

	wrappedKey, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return parsedDataEncryptionEnvelope{}, true, errors.New("malformed wrapped key")
	}
	dataPayload, err := base64.RawURLEncoding.DecodeString(parts[4])
	if err != nil {
		return parsedDataEncryptionEnvelope{}, true, errors.New("malformed encrypted payload")
	}

	block, err := aes.NewCipher(make([]byte, dataEncryptionKeySize))
	if err != nil {
		return parsedDataEncryptionEnvelope{}, true, errors.New("initialize envelope validation")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return parsedDataEncryptionEnvelope{}, true, errors.New("initialize envelope validation")
	}
	if len(wrappedKey) != gcm.NonceSize()+dataEncryptionKeySize+gcm.Overhead() {
		return parsedDataEncryptionEnvelope{}, true, errors.New("invalid wrapped key length")
	}
	if len(dataPayload) < gcm.NonceSize()+gcm.Overhead() {
		return parsedDataEncryptionEnvelope{}, true, errors.New("invalid encrypted payload length")
	}

	return parsedDataEncryptionEnvelope{
		keyID:       parts[2],
		wrappedKey:  wrappedKey,
		dataPayload: dataPayload,
	}, true, nil
}

func unwrapDataEncryptionKey(
	domain string,
	envelope parsedDataEncryptionEnvelope,
) ([dataEncryptionKeySize]byte, error) {
	var dataKey [dataEncryptionKeySize]byte
	config := currentDataEncryptionConfig()
	rootKey, exists := config.keys[envelope.keyID]
	if !exists {
		return dataKey, fmt.Errorf(
			"data encryption key identifier is unavailable for domain %s",
			domain,
		)
	}
	plaintextKey, err := openDataEncryptionBytes(
		rootKey[:],
		envelope.wrappedKey,
		dataEncryptionWrapAAD(domain, envelope.keyID),
	)
	if err != nil || len(plaintextKey) != dataEncryptionKeySize {
		return dataKey, fmt.Errorf("authenticate wrapped key for domain %s", domain)
	}
	copy(dataKey[:], plaintextKey)
	return dataKey, nil
}

func sealDataEncryptionBytes(key []byte, plaintext []byte, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, aad), nil
}

func openDataEncryptionBytes(key []byte, payload []byte, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(payload) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("encrypted payload is too short")
	}
	nonce := payload[:gcm.NonceSize()]
	ciphertext := payload[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, aad)
}

func dataEncryptionPayloadAAD(domain string) []byte {
	return []byte("new-api:data-encryption:v1:payload:" + domain)
}

func dataEncryptionWrapAAD(domain string, keyID string) []byte {
	return []byte("new-api:data-encryption:v1:wrap:" + domain + ":" + keyID)
}

func validDataEncryptionKeyID(keyID string) bool {
	if len(keyID) == 0 || len(keyID) > dataEncryptionKeyIDMaxLength {
		return false
	}
	for _, char := range keyID {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' ||
			char == '_' {
			continue
		}
		return false
	}
	return true
}
