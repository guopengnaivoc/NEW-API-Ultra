package types

import "fmt"

// FileSourceErrorCategory identifies a public-safe file processing failure.
type FileSourceErrorCategory string

const (
	FileSourceErrorCategoryInvalidSource    FileSourceErrorCategory = "invalid_source"
	FileSourceErrorCategoryUnsupported      FileSourceErrorCategory = "unsupported_source"
	FileSourceErrorCategoryAccessRejected   FileSourceErrorCategory = "access_rejected"
	FileSourceErrorCategoryDownloadFailed   FileSourceErrorCategory = "download_failed"
	FileSourceErrorCategoryUnexpectedStatus FileSourceErrorCategory = "unexpected_status"
	FileSourceErrorCategoryReadFailed       FileSourceErrorCategory = "read_failed"
	FileSourceErrorCategoryTooLarge         FileSourceErrorCategory = "too_large"
	FileSourceErrorCategoryInvalidBase64    FileSourceErrorCategory = "invalid_base64"
	FileSourceErrorCategoryCacheReadFailed  FileSourceErrorCategory = "cache_read_failed"
	FileSourceErrorCategoryInvalidContent   FileSourceErrorCategory = "invalid_content"
	FileSourceErrorCategoryInternal         FileSourceErrorCategory = "internal"
)

// FileSourceError is safe to expose in API responses and request-correlated
// logs. It deliberately does not retain or unwrap the original error because
// URL parsers, HTTP transports, and disk operations commonly embed raw secrets.
type FileSourceError struct {
	category   FileSourceErrorCategory
	identifier string
	detail     int
}

func NewFileSourceError(category FileSourceErrorCategory, source FileSource, detail ...int) *FileSourceError {
	numericDetail := 0
	if len(detail) > 0 {
		numericDetail = detail[0]
	}
	identifier := "file:unknown"
	switch knownSource := source.(type) {
	case *URLSource:
		if knownSource != nil {
			identifier = knownSource.GetIdentifier()
		}
	case *Base64Source:
		if knownSource != nil {
			identifier = knownSource.GetIdentifier()
		}
	}
	return &FileSourceError{
		category:   category,
		identifier: identifier,
		detail:     numericDetail,
	}
}

func (e *FileSourceError) Category() FileSourceErrorCategory {
	if e == nil {
		return FileSourceErrorCategoryInternal
	}
	return e.category
}

func (e *FileSourceError) Identifier() string {
	if e == nil || e.identifier == "" {
		return "file:unknown"
	}
	return e.identifier
}

func (e *FileSourceError) Detail() int {
	if e == nil {
		return 0
	}
	return e.detail
}

func (e *FileSourceError) Error() string {
	if e == nil {
		return "file source operation failed: file:unknown"
	}

	message := "file source operation failed"
	switch e.category {
	case FileSourceErrorCategoryInvalidSource:
		message = "file source is invalid"
	case FileSourceErrorCategoryUnsupported:
		message = "file source type is unsupported"
	case FileSourceErrorCategoryAccessRejected:
		message = "file source access rejected"
	case FileSourceErrorCategoryDownloadFailed:
		message = "file source download failed"
	case FileSourceErrorCategoryUnexpectedStatus:
		message = fmt.Sprintf("file source returned unexpected status %d", e.detail)
	case FileSourceErrorCategoryReadFailed:
		message = "file source read failed"
	case FileSourceErrorCategoryTooLarge:
		message = fmt.Sprintf("file source exceeds size limit of %d MB", e.detail)
	case FileSourceErrorCategoryInvalidBase64:
		message = "file source contains invalid base64 data"
	case FileSourceErrorCategoryCacheReadFailed:
		message = "file source cache read failed"
	case FileSourceErrorCategoryInvalidContent:
		message = "file source content is invalid"
	}
	return fmt.Sprintf("%s: %s", message, e.Identifier())
}
