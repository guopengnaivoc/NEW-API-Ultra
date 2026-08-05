package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/baidu"
	"github.com/QuantumNous/new-api/relay/channel/cloudflare"
	"github.com/QuantumNous/new-api/relay/channel/cohere"
	"github.com/QuantumNous/new-api/relay/channel/dify"
	"github.com/QuantumNous/new-api/relay/channel/jina"
	"github.com/QuantumNous/new-api/relay/channel/mistral"
	"github.com/QuantumNous/new-api/relay/channel/mokaai"
	"github.com/QuantumNous/new-api/relay/channel/palm"
	"github.com/QuantumNous/new-api/relay/channel/tencent"
	"github.com/QuantumNous/new-api/relay/channel/xunfei"
	"github.com/QuantumNous/new-api/relay/channel/zhipu"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnsupportedAdaptorConvertClaudeRequestDoesNotPanic(t *testing.T) {
	req := &dto.ClaudeRequest{}
	info := &relaycommon.RelayInfo{}

	tests := []struct {
		name string
		adpt func() channel.Adaptor
	}{
		{"cohere", func() channel.Adaptor { return &cohere.Adaptor{} }},
		{"baidu", func() channel.Adaptor { return &baidu.Adaptor{} }},
		{"jina", func() channel.Adaptor { return &jina.Adaptor{} }},
		{"palm", func() channel.Adaptor { return &palm.Adaptor{} }},
		{"mistral", func() channel.Adaptor { return &mistral.Adaptor{} }},
		{"dify", func() channel.Adaptor { return &dify.Adaptor{} }},
		{"tencent", func() channel.Adaptor { return &tencent.Adaptor{} }},
		{"xunfei", func() channel.Adaptor { return &xunfei.Adaptor{} }},
		{"zhipu", func() channel.Adaptor { return &zhipu.Adaptor{} }},
		{"cloudflare", func() channel.Adaptor { return &cloudflare.Adaptor{} }},
		{"mokaai", func() channel.Adaptor { return &mokaai.Adaptor{} }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := tc.adpt()
			var got any
			var err error
			require.NotPanics(t, func() {
				got, err = target.ConvertClaudeRequest(nil, info, req)
			})
			assert.Nil(t, got)
			require.Error(t, err)
			assert.Equal(t, "not implemented", err.Error())
		})
	}
}
