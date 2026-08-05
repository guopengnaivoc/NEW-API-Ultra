package converror

import "errors"

type kind string

const (
	clientInput      kind = "client_input"
	upstreamResponse kind = "upstream_response"
)

type conversionError struct {
	kind kind
	err  error
}

func (e *conversionError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *conversionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func ClientInput(err error) error {
	return wrap(clientInput, err)
}

func UpstreamResponse(err error) error {
	return wrap(upstreamResponse, err)
}

func IsClientInput(err error) bool {
	return isKind(err, clientInput)
}

func IsUpstreamResponse(err error) bool {
	return isKind(err, upstreamResponse)
}

func wrap(target kind, err error) error {
	if err == nil {
		return nil
	}
	var conversionErr *conversionError
	if errors.As(err, &conversionErr) && conversionErr.kind == target {
		return err
	}
	return &conversionError{kind: target, err: err}
}

func isKind(err error, target kind) bool {
	var conversionErr *conversionError
	return errors.As(err, &conversionErr) && conversionErr.kind == target
}
