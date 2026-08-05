package relay

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func TestMain(m *testing.M) {
	if err := os.Setenv(
		"DATA_ENCRYPTION_KEYS",
		"test=YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE=",
	); err != nil {
		panic("failed to set test data encryption key: " + err.Error())
	}
	if err := os.Setenv("DATA_ENCRYPTION_ACTIVE_KEY_ID", "test"); err != nil {
		panic("failed to set active test data encryption key: " + err.Error())
	}
	if err := os.Setenv("DATA_ENCRYPTION_ENABLE", "true"); err != nil {
		panic("failed to enable test data encryption: " + err.Error())
	}
	if err := common.InitDataEncryption(); err != nil {
		panic("failed to initialize test data encryption: " + err.Error())
	}

	os.Exit(m.Run())
}
