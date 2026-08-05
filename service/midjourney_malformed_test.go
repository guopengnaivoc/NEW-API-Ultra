package service

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetMjRequestModelRejectsNilRequestWithoutPanic(t *testing.T) {
	var (
		modelName string
		response  *dto.MidjourneyResponse
		ok        bool
	)

	assert.NotPanics(t, func() {
		modelName, response, ok = GetMjRequestModel(
			relayconstant.RelayModeMidjourneyAction,
			nil,
		)
	})

	assert.Empty(t, modelName)
	require.NotNil(t, response)
	assert.Equal(t, constant.MjRequestError, response.Code)
	assert.Equal(t, "invalid_request", response.Description)
	assert.False(t, ok)
}

func TestCoverPlusActionRejectsMalformedCustomIDWithoutPanic(t *testing.T) {
	testCases := []struct {
		name        string
		customID    string
		description string
	}{
		{name: "missing separators", customID: "MJ", description: "unknown_action:MJ"},
		{name: "missing job action", customID: "MJ::JOB", description: "unknown_action:MJ::JOB"},
		{name: "empty action", customID: "MJ::::", description: "unknown_action"},
		{name: "missing upsample index", customID: "MJ::JOB::upsample", description: "index_parse_failed"},
		{name: "missing variation index", customID: "MJ::variation", description: "index_parse_failed"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			request := &dto.MidjourneyRequest{CustomId: testCase.customID}
			var response *dto.MidjourneyResponse

			assert.NotPanics(t, func() {
				response = CoverPlusActionToNormalAction(request)
			})

			require.NotNil(t, response)
			assert.Equal(t, constant.MjRequestError, response.Code)
			assert.Equal(t, testCase.description, response.Description)
			assert.Empty(t, request.Action)
			assert.Zero(t, request.Index)
		})
	}
}

func TestConvertSimpleChangeParamsRejectsShortActionWithoutPanic(t *testing.T) {
	for _, content := range []string{
		"task ",
		"task u",
		"task v",
	} {
		t.Run(content, func(t *testing.T) {
			var request *dto.MidjourneyRequest

			assert.NotPanics(t, func() {
				request = ConvertSimpleChangeParams(content)
			})

			assert.Nil(t, request)
		})
	}
}

func TestMidjourneyActionParsingPreservesValidInputs(t *testing.T) {
	testCases := []struct {
		name     string
		customID string
		action   string
		index    int
	}{
		{
			name:     "upsample",
			customID: "MJ::JOB::upsample::2::task",
			action:   constant.MjActionUpscale,
			index:    2,
		},
		{
			name:     "variation",
			customID: "MJ::JOB::variation::3::task",
			action:   constant.MjActionVariation,
			index:    3,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			request := &dto.MidjourneyRequest{CustomId: testCase.customID}

			response := CoverPlusActionToNormalAction(request)

			require.Nil(t, response)
			assert.Equal(t, testCase.action, request.Action)
			assert.Equal(t, testCase.index, request.Index)
		})
	}

	for _, testCase := range []struct {
		content string
		action  string
		index   int
	}{
		{content: "task u1", action: "UPSCALE", index: 1},
		{content: "task v4", action: "VARIATION", index: 4},
		{content: "task r", action: "REROLL", index: 0},
	} {
		request := ConvertSimpleChangeParams(testCase.content)
		require.NotNil(t, request)
		assert.Equal(t, testCase.action, request.Action)
		assert.Equal(t, testCase.index, request.Index)
	}
}
