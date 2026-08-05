package openai

import (
	"fmt"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func OpenaiRealtimeHandler(c *gin.Context, info *relaycommon.RelayInfo) (*types.NewAPIError, *dto.RealtimeUsage) {
	if info == nil || info.ClientWs == nil || info.TargetWs == nil {
		return types.NewError(fmt.Errorf("invalid websocket connection"), types.ErrorCodeBadResponse), nil
	}

	info.IsStream = true
	clientConn := info.ClientWs
	targetConn := info.TargetWs

	type realtimeMessage struct {
		fromClient bool
		payload    []byte
	}

	messages := make(chan realtimeMessage)
	readerDone := make(chan error, 2)
	stopReaders := make(chan struct{})
	var readers sync.WaitGroup

	usage := &dto.RealtimeUsage{}
	localUsage := &dto.RealtimeUsage{}
	sumUsage := &dto.RealtimeUsage{}

	reportReaderDone := func(err error) {
		select {
		case readerDone <- err:
		case <-stopReaders:
		}
	}
	readMessages := func(name string, conn *websocket.Conn, fromClient bool) {
		defer readers.Done()
		defer func() {
			if r := recover(); r != nil {
				reportReaderDone(fmt.Errorf("panic in %s reader: %v", name, r))
			}
		}()

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					reportReaderDone(nil)
				} else {
					reportReaderDone(fmt.Errorf("error reading from %s: %v", name, err))
				}
				return
			}

			select {
			case messages <- realtimeMessage{fromClient: fromClient, payload: message}:
			case <-stopReaders:
				return
			case <-c.Done():
				return
			}
		}
	}

	readers.Add(2)
	gopool.Go(func() {
		readMessages("client", clientConn, true)
	})
	gopool.Go(func() {
		readMessages("target", targetConn, false)
	})

	running := true
	for running {
		select {
		case message := <-messages:
			realtimeEvent := &dto.RealtimeEvent{}
			if err := common.Unmarshal(message.payload, realtimeEvent); err != nil {
				logger.LogError(c, "realtime error: error unmarshalling message: "+err.Error())
				running = false
				continue
			}

			if message.fromClient {
				if realtimeEvent.Type == dto.RealtimeEventTypeSessionUpdate &&
					realtimeEvent.Session != nil &&
					realtimeEvent.Session.Tools != nil {
					info.RealtimeTools = realtimeEvent.Session.Tools
				}

				textToken, audioToken, err := service.CountTokenRealtime(info, *realtimeEvent, info.UpstreamModelName)
				if err != nil {
					logger.LogError(c, "realtime error: error counting client token: "+err.Error())
					running = false
					continue
				}
				logger.LogInfo(c, fmt.Sprintf("type: %s, textToken: %d, audioToken: %d", realtimeEvent.Type, textToken, audioToken))
				localUsage.TotalTokens += textToken + audioToken
				localUsage.InputTokens += textToken + audioToken
				localUsage.InputTokenDetails.TextTokens += textToken
				localUsage.InputTokenDetails.AudioTokens += audioToken

				if err := helper.WssString(c, targetConn, string(message.payload)); err != nil {
					logger.LogError(c, "realtime error: error writing to target: "+err.Error())
					running = false
				}
				continue
			}

			info.SetFirstResponseTime()
			if realtimeEvent.Type == dto.RealtimeEventTypeResponseDone {
				if realtimeEvent.Response == nil {
					logger.LogError(c, "realtime error: response.done event is missing response data")
					running = false
					continue
				}

				realtimeUsage := realtimeEvent.Response.Usage
				if realtimeUsage != nil {
					usage.TotalTokens += realtimeUsage.TotalTokens
					usage.InputTokens += realtimeUsage.InputTokens
					usage.OutputTokens += realtimeUsage.OutputTokens
					usage.InputTokenDetails.AudioTokens += realtimeUsage.InputTokenDetails.AudioTokens
					usage.InputTokenDetails.CachedTokens += realtimeUsage.InputTokenDetails.CachedTokens
					usage.InputTokenDetails.TextTokens += realtimeUsage.InputTokenDetails.TextTokens
					usage.OutputTokenDetails.AudioTokens += realtimeUsage.OutputTokenDetails.AudioTokens
					usage.OutputTokenDetails.TextTokens += realtimeUsage.OutputTokenDetails.TextTokens
					// Authoritative response usage supersedes the local estimate
					// even when reserving that usage fails.
					localUsage = &dto.RealtimeUsage{}
					if err := preConsumeUsage(c, info, usage, sumUsage); err != nil {
						logger.LogError(c, "realtime error: error consuming upstream usage: "+err.Error())
						running = false
						continue
					}
				} else {
					textToken, audioToken, err := service.CountTokenRealtime(info, *realtimeEvent, info.UpstreamModelName)
					if err != nil {
						logger.LogError(c, "realtime error: error counting response token: "+err.Error())
						running = false
						continue
					}
					logger.LogInfo(c, fmt.Sprintf("type: %s, textToken: %d, audioToken: %d", realtimeEvent.Type, textToken, audioToken))
					localUsage.TotalTokens += textToken + audioToken
					info.IsFirstRequest = false
					localUsage.InputTokens += textToken + audioToken
					localUsage.InputTokenDetails.TextTokens += textToken
					localUsage.InputTokenDetails.AudioTokens += audioToken
					if err := preConsumeUsage(c, info, localUsage, sumUsage); err != nil {
						logger.LogError(c, "realtime error: error consuming local usage: "+err.Error())
						running = false
						continue
					}
				}
				logger.LogInfo(c, fmt.Sprintf("realtime streaming sumUsage: %v", sumUsage))
				logger.LogInfo(c, fmt.Sprintf("realtime streaming localUsage: %v", localUsage))
			} else if realtimeEvent.Type == dto.RealtimeEventTypeSessionUpdated ||
				realtimeEvent.Type == dto.RealtimeEventTypeSessionCreated {
				if realtimeEvent.Session != nil {
					info.InputAudioFormat = common.GetStringIfEmpty(realtimeEvent.Session.InputAudioFormat, info.InputAudioFormat)
					info.OutputAudioFormat = common.GetStringIfEmpty(realtimeEvent.Session.OutputAudioFormat, info.OutputAudioFormat)
				}
			} else {
				textToken, audioToken, err := service.CountTokenRealtime(info, *realtimeEvent, info.UpstreamModelName)
				if err != nil {
					logger.LogError(c, "realtime error: error counting target token: "+err.Error())
					running = false
					continue
				}
				logger.LogInfo(c, fmt.Sprintf("type: %s, textToken: %d, audioToken: %d", realtimeEvent.Type, textToken, audioToken))
				localUsage.TotalTokens += textToken + audioToken
				localUsage.OutputTokens += textToken + audioToken
				localUsage.OutputTokenDetails.TextTokens += textToken
				localUsage.OutputTokenDetails.AudioTokens += audioToken
			}

			if err := helper.WssString(c, clientConn, string(message.payload)); err != nil {
				logger.LogError(c, "realtime error: error writing to client: "+err.Error())
				running = false
			}
		case err := <-readerDone:
			if err != nil {
				logger.LogError(c, "realtime error: "+err.Error())
			}
			running = false
		case <-c.Done():
			running = false
		}
	}

	// Closing both connections unblocks the remaining reader. Wait for both
	// readers before inspecting or billing their shared session state.
	close(stopReaders)
	_ = clientConn.Close()
	_ = targetConn.Close()
	readers.Wait()

	if usage.TotalTokens != 0 {
		_ = preConsumeUsage(c, info, usage, sumUsage)
	}

	if localUsage.TotalTokens != 0 {
		_ = preConsumeUsage(c, info, localUsage, sumUsage)
	}

	return nil, sumUsage
}

func preConsumeUsage(ctx *gin.Context, info *relaycommon.RelayInfo, usage *dto.RealtimeUsage, totalUsage *dto.RealtimeUsage) error {
	if usage == nil || totalUsage == nil {
		return fmt.Errorf("invalid usage pointer")
	}

	totalUsage.TotalTokens += usage.TotalTokens
	totalUsage.InputTokens += usage.InputTokens
	totalUsage.OutputTokens += usage.OutputTokens
	totalUsage.InputTokenDetails.CachedTokens += usage.InputTokenDetails.CachedTokens
	totalUsage.InputTokenDetails.TextTokens += usage.InputTokenDetails.TextTokens
	totalUsage.InputTokenDetails.AudioTokens += usage.InputTokenDetails.AudioTokens
	totalUsage.OutputTokenDetails.TextTokens += usage.OutputTokenDetails.TextTokens
	totalUsage.OutputTokenDetails.AudioTokens += usage.OutputTokenDetails.AudioTokens
	*usage = dto.RealtimeUsage{}
	// Reserve against cumulative usage; final settlement remains the only
	// operation that converts the reservation into the actual charge.
	err := service.PreWssConsumeQuota(ctx, info, totalUsage)
	return err
}
