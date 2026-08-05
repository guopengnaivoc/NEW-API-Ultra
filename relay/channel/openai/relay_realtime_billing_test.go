package openai

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingRealtimeBillingSession struct {
	reservations []int
	reserveError error
}

type acceptedWebsocket struct {
	conn *websocket.Conn
	err  error
}

func realtimeRatioPriceData() types.PriceData {
	return types.PriceData{
		ModelRatio:           1,
		CompletionRatio:      1,
		AudioRatio:           1,
		AudioCompletionRatio: 1,
		GroupRatioInfo: types.GroupRatioInfo{
			GroupRatio: 1,
		},
	}
}

func newRealtimeWebsocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()

	serverConn := make(chan acceptedWebsocket, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		}).Upgrade(w, r, nil)
		serverConn <- acceptedWebsocket{conn: conn, err: err}
	}))
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientConn.Close() })

	accepted := <-serverConn
	require.NoError(t, accepted.err)
	t.Cleanup(func() { _ = accepted.conn.Close() })
	return accepted.conn, clientConn
}

func (s *recordingRealtimeBillingSession) Settle(int) error {
	return nil
}

func (s *recordingRealtimeBillingSession) Refund(*gin.Context) {}

func (s *recordingRealtimeBillingSession) NeedsRefund() bool {
	return true
}

func (s *recordingRealtimeBillingSession) GetPreConsumedQuota() int {
	return 0
}

func (s *recordingRealtimeBillingSession) Reserve(targetQuota int) error {
	s.reservations = append(s.reservations, targetQuota)
	return s.reserveError
}

func TestRealtimeProgressiveReservationUsesCumulativeUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(nil)
	billing := &recordingRealtimeBillingSession{}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-4.1",
		UsingGroup:      "default",
		Billing:         billing,
		PriceData:       realtimeRatioPriceData(),
	}
	total := &dto.RealtimeUsage{}

	for range 2 {
		segment := &dto.RealtimeUsage{
			TotalTokens: 10,
			InputTokens: 10,
			InputTokenDetails: dto.InputTokenDetails{
				TextTokens: 10,
			},
		}
		require.NoError(t, preConsumeUsage(context, info, segment, total))
		assert.Zero(t, segment.TotalTokens)
	}

	assert.Equal(t, 20, total.TotalTokens)
	assert.Equal(t, 20, total.InputTokens)
	require.Len(t, billing.reservations, 2)
	assert.Positive(t, billing.reservations[0])
	assert.Equal(t, billing.reservations[0]*2, billing.reservations[1])
}

func TestRealtimeReservationFailureDoesNotLeaveUsageForDoubleCounting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(nil)
	reservationError := errors.New("reservation failed")
	billing := &recordingRealtimeBillingSession{
		reserveError: reservationError,
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-4.1",
		UsingGroup:      "default",
		Billing:         billing,
		PriceData:       realtimeRatioPriceData(),
	}
	total := &dto.RealtimeUsage{}
	segment := &dto.RealtimeUsage{
		TotalTokens: 10,
		InputTokens: 10,
		InputTokenDetails: dto.InputTokenDetails{
			TextTokens: 10,
		},
	}

	err := preConsumeUsage(context, info, segment, total)

	require.ErrorIs(t, err, reservationError)
	assert.Equal(t, 10, total.TotalTokens)
	assert.Zero(t, segment.TotalTokens)
}

func TestRealtimeAuthoritativeUsageDiscardsSupersededEstimateOnReserveFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	clientConn, _ := newRealtimeWebsocketPair(t)
	targetPeer, targetConn := newRealtimeWebsocketPair(t)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	reservationError := errors.New("reservation capacity exhausted")
	billing := &recordingRealtimeBillingSession{
		reserveError: reservationError,
	}
	info := &relaycommon.RelayInfo{
		ClientWs:        clientConn,
		TargetWs:        targetConn,
		OriginModelName: "gpt-4.1",
		UsingGroup:      "default",
		Billing:         billing,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-test",
		},
		InputAudioFormat:  "pcm16",
		OutputAudioFormat: "pcm16",
		PriceData:         realtimeRatioPriceData(),
	}

	result := make(chan *dto.RealtimeUsage, 1)
	go func() {
		_, usage := OpenaiRealtimeHandler(context, info)
		result <- usage
	}()

	require.NoError(t, targetPeer.WriteMessage(
		websocket.TextMessage,
		[]byte(`{"type":"response.audio_transcript.delta","delta":"overlapping local estimate"}`),
	))
	require.NoError(t, targetPeer.WriteMessage(
		websocket.TextMessage,
		[]byte(`{"type":"response.done","response":{"usage":{"total_tokens":10,"input_tokens":10,"output_tokens":0,"input_token_details":{"text_tokens":10}}}}`),
	))

	var finalUsage *dto.RealtimeUsage
	select {
	case finalUsage = <-result:
	case <-time.After(time.Second):
		require.Fail(t, "realtime handler did not stop after reservation failure")
	}

	require.NotNil(t, finalUsage)
	assert.Equal(t, 10, finalUsage.TotalTokens)
	assert.Equal(t, 10, finalUsage.InputTokens)
	assert.Zero(t, finalUsage.OutputTokens)
	require.Len(t, billing.reservations, 1)
}

func TestRealtimeHandlerStopsBothReadersBeforeReturning(t *testing.T) {
	gin.SetMode(gin.TestMode)

	clientConn, clientPeer := newRealtimeWebsocketPair(t)
	targetPeer, targetConn := newRealtimeWebsocketPair(t)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	info := &relaycommon.RelayInfo{
		ClientWs: clientConn,
		TargetWs: targetConn,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4o",
		},
		InputAudioFormat:  "pcm16",
		OutputAudioFormat: "pcm16",
		PriceData: types.PriceData{
			FreeModel: true,
		},
	}

	result := make(chan *dto.RealtimeUsage, 1)
	go func() {
		_, usage := OpenaiRealtimeHandler(context, info)
		result <- usage
	}()

	start := make(chan struct{})
	writeErrors := make(chan error, 2)
	go func() {
		<-start
		writeErrors <- clientPeer.WriteMessage(websocket.TextMessage, []byte(
			`{"type":"session.update","session":{"instructions":"client input"}}`,
		))
	}()
	go func() {
		<-start
		writeErrors <- targetPeer.WriteMessage(websocket.TextMessage, []byte(
			`{"type":"response.audio_transcript.delta","delta":"target output"}`,
		))
	}()
	close(start)
	require.NoError(t, <-writeErrors)
	require.NoError(t, <-writeErrors)

	require.NoError(t, targetPeer.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err := targetPeer.ReadMessage()
	require.NoError(t, err)
	require.NoError(t, clientPeer.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err = clientPeer.ReadMessage()
	require.NoError(t, err)

	require.NoError(t, targetPeer.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second),
	))
	var finalUsage *dto.RealtimeUsage
	select {
	case finalUsage = <-result:
	case <-time.After(time.Second):
		require.Fail(t, "realtime handler did not return after its target closed")
	}
	require.NotNil(t, finalUsage)
	assert.Positive(t, finalUsage.InputTokenDetails.TextTokens)
	assert.Positive(t, finalUsage.OutputTokenDetails.TextTokens)
	assert.Equal(t, finalUsage.InputTokens+finalUsage.OutputTokens, finalUsage.TotalTokens)

	require.NoError(t, clientPeer.SetReadDeadline(time.Now().Add(250*time.Millisecond)))
	_, _, err = clientPeer.ReadMessage()
	require.Error(t, err)
	var networkError net.Error
	if errors.As(err, &networkError) {
		assert.False(t, networkError.Timeout(), "handler returned while the client reader was still blocked")
	}
}
