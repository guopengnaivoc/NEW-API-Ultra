package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/ionet"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ioNetContextCapturingTransport struct {
	context context.Context
}

func (c *ioNetContextCapturingTransport) Do(req *ionet.HTTPRequest) (*ionet.HTTPResponse, error) {
	c.context = req.Context
	return &ionet.HTTPResponse{
		StatusCode: http.StatusOK,
		Body:       []byte(`{"data":{"hardware":[],"total":0}}`),
	}, nil
}

func TestIoNetControllerClientsBindInboundRequestContext(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	optionMapWasNil := common.OptionMap == nil
	if optionMapWasNil {
		common.OptionMap = make(map[string]string)
	}
	previousEnabled, hadEnabled := common.OptionMap["model_deployment.ionet.enabled"]
	previousAPIKey, hadAPIKey := common.OptionMap["model_deployment.ionet.api_key"]
	common.OptionMap["model_deployment.ionet.enabled"] = "true"
	common.OptionMap["model_deployment.ionet.api_key"] = "test-key"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if optionMapWasNil {
			common.OptionMap = nil
			return
		}
		if hadEnabled {
			common.OptionMap["model_deployment.ionet.enabled"] = previousEnabled
		} else {
			delete(common.OptionMap, "model_deployment.ionet.enabled")
		}
		if hadAPIKey {
			common.OptionMap["model_deployment.ionet.api_key"] = previousAPIKey
		} else {
			delete(common.OptionMap, "model_deployment.ionet.api_key")
		}
	})

	requestContext, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/deployment", nil).WithContext(requestContext)
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginContext.Request = request

	tests := []struct {
		name      string
		newClient func(*gin.Context) (*ionet.Client, bool)
	}{
		{name: "public", newClient: getIoClient},
		{name: "enterprise", newClient: getIoEnterpriseClient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, ok := tt.newClient(ginContext)
			require.True(t, ok)
			transport := &ioNetContextCapturingTransport{}
			client.HTTPClient = transport

			_, err := client.GetMaxGPUsPerContainer()
			require.NoError(t, err)
			assert.Equal(t, requestContext, transport.context)
		})
	}

	cancel()
	assert.ErrorIs(t, requestContext.Err(), context.Canceled)
}
