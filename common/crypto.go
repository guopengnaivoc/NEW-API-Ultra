package common

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
)

func GenerateHMACWithKey(key []byte, data string) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func GenerateHMAC(data string) string {
	h := hmac.New(sha256.New, []byte(CryptoSecret))
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func Password2Hash(password string) (string, error) {
	passwordBytes := []byte(password)
	hashedPassword, err := bcrypt.GenerateFromPassword(passwordBytes, bcrypt.DefaultCost)
	return string(hashedPassword), err
}

func ValidatePasswordAndHash(password string, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// dummyPasswordHash is a bcrypt hash of an unguessable random value, used
// only to equalize response timing on login attempts against accounts that
// do not exist (anti account-enumeration).
var dummyPasswordHash, _ = bcrypt.GenerateFromPassword([]byte("new-api-timing-equalizer"), bcrypt.DefaultCost)

// BurnPasswordCheckTime performs one bcrypt comparison that always fails,
// so "no such account" and "wrong password" take comparable time.
func BurnPasswordCheckTime(password string) {
	_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
}
