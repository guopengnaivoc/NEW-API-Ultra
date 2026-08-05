package model

import (
	"bytes"
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestLogPaymentTopUpFailureOmitsDatabaseErrorMessage(t *testing.T) {
	var output bytes.Buffer
	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &output
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = previousWriter
		common.LogWriterMu.Unlock()
	})

	sentinel := "duplicate email customer-log-sentinel@example.com"
	logPaymentTopUpFailure(PaymentProviderCreem, errors.New(sentinel))

	assert.Contains(t, output.String(), "provider=creem")
	assert.Contains(t, output.String(), "error_type=")
	assert.NotContains(t, output.String(), sentinel)
	assert.NotContains(t, output.String(), "customer-log-sentinel@example.com")
}
