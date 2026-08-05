package controller

import (
	"strconv"
	"unicode/utf8"
)

const (
	paymentWebhookLogFieldMaxBytes    = 128
	paymentWebhookLogTruncationMarker = "...[truncated]"
)

func paymentWebhookLogField(value string) string {
	if len(value) <= paymentWebhookLogFieldMaxBytes {
		return strconv.QuoteToASCII(value)
	}

	end := paymentWebhookLogFieldMaxBytes
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	value = value[:end] + paymentWebhookLogTruncationMarker
	return strconv.QuoteToASCII(value)
}
