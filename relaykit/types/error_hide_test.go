package types

import (
	"errors"
	"os"
	"os/exec"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrOptionWithHideErrMsgDoesNotPrintOriginError(t *testing.T) {
	cmd := exec.Command(
		os.Args[0],
		"-test.run=^TestErrOptionWithHideErrMsgDebugHelper$",
	)
	cmd.Env = append(
		os.Environ(),
		"NEWAPI_TEST_HIDE_ERROR_DEBUG_HELPER=1",
	)

	output, err := cmd.CombinedOutput()

	require.NoError(t, err, string(output))
	assert.NotContains(t, string(output), "TOPSECRETSIGNATURE")
	assert.NotContains(t, string(output), "private/customer-123")
}

func TestErrOptionWithHideErrMsgDebugHelper(t *testing.T) {
	if os.Getenv("NEWAPI_TEST_HIDE_ERROR_DEBUG_HELPER") != "1" {
		return
	}
	kitutil.Debug.Store(true)
	t.Cleanup(func() {
		kitutil.Debug.Store(false)
	})

	err := NewError(
		errors.New(
			"request failed for https://files.example.com/private/customer-123"+
				"?X-Amz-Signature=TOPSECRETSIGNATURE",
		),
		ErrorCodeDoRequestFailed,
		ErrOptionWithHideErrMsg("upstream error: do request failed"),
	)

	assert.Equal(t, "upstream error: do request failed", err.Error())
}
