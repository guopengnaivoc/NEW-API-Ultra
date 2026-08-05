package relay

import (
	"net/http"

	"github.com/QuantumNous/new-api/relaykit/relayconvert/converror"
	"github.com/QuantumNous/new-api/relaykit/types"
)

func newRequestConversionError(err error) *types.NewAPIError {
	if converror.IsClientInput(err) {
		return types.NewErrorWithStatusCode(
			err,
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	return types.NewError(
		err,
		types.ErrorCodeConvertRequestFailed,
		types.ErrOptionWithSkipRetry(),
	)
}
