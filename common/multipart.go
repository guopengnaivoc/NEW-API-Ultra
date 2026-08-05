package common

import "strings"

// multipartHeaderEscaper mirrors mime/multipart's escapeQuotes and
// additionally strips CR/LF so client-controlled filenames and field names
// cannot terminate a Content-Disposition header or inject new MIME headers
// when parts are rebuilt for upstream providers.
var multipartHeaderEscaper = strings.NewReplacer(
	"\\", "\\\\",
	`"`, "\\\"",
	"\r", "",
	"\n", "",
)

// EscapeMultipartHeaderValue sanitizes a value for use inside a quoted
// Content-Disposition parameter.
func EscapeMultipartHeaderValue(value string) string {
	return multipartHeaderEscaper.Replace(value)
}
