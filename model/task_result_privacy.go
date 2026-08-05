package model

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/geminitaskresult"
)

const taskProviderResultURIDomain = "tasks:provider_result_uri"

func (t *Task) SetProviderResultURI(uri string) (bool, error) {
	if uri == "" {
		changed := t.EncryptedProviderResultURI != nil
		t.ClearProviderResultURI()
		return changed, nil
	}
	if len(uri) > geminitaskresult.MaxProviderResultURIBytes {
		return false, fmt.Errorf(
			"task provider result URI exceeds %d bytes",
			geminitaskresult.MaxProviderResultURIBytes,
		)
	}

	if t.EncryptedProviderResultURI != nil &&
		*t.EncryptedProviderResultURI != "" {
		existing, err := t.OpenProviderResultURI()
		if err != nil {
			return false, err
		}
		if existing == uri {
			return false, nil
		}
	}

	stored, err := common.SealDataEncryptionValueRequired(
		taskProviderResultURIDomain,
		uri,
	)
	if err != nil {
		return false, fmt.Errorf("seal task provider result URI: %w", err)
	}
	t.EncryptedProviderResultURI = &stored
	return true, nil
}

func (t *Task) OpenProviderResultURI() (string, error) {
	if t.EncryptedProviderResultURI == nil ||
		*t.EncryptedProviderResultURI == "" {
		return "", nil
	}

	stored := *t.EncryptedProviderResultURI
	if !common.IsDataEncryptionEnvelope(stored) {
		return "", fmt.Errorf(
			"open task provider result URI: stored value is not an encrypted envelope for domain %s",
			taskProviderResultURIDomain,
		)
	}
	plaintext, info, err := common.OpenDataEncryptionValue(
		taskProviderResultURIDomain,
		stored,
	)
	if err != nil {
		return "", fmt.Errorf("open task provider result URI: %w", err)
	}
	if !info.Encrypted {
		return "", fmt.Errorf(
			"open task provider result URI: stored value is not encrypted for domain %s",
			taskProviderResultURIDomain,
		)
	}
	if len(plaintext) > geminitaskresult.MaxProviderResultURIBytes {
		return "", fmt.Errorf(
			"open task provider result URI: plaintext exceeds %d bytes",
			geminitaskresult.MaxProviderResultURIBytes,
		)
	}
	return plaintext, nil
}

func (t *Task) ClearProviderResultURI() {
	t.EncryptedProviderResultURI = nil
}

func (t *Task) IsGeminiTask() bool {
	if t == nil {
		return false
	}
	return t.Platform == constant.TaskPlatform(
		strconv.Itoa(constant.ChannelTypeGemini),
	) || strings.EqualFold(string(t.Platform), "gemini")
}
