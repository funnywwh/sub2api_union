package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayService_ProxyResponsesWebSocketFromClient_ContextCancelDisconnectsClient(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		t.Run(mode, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.Enabled = true
			cfg.Gateway.OpenAIWS.OAuthEnabled = true
			cfg.Gateway.OpenAIWS.APIKeyEnabled = true
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
			cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
			cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 30
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

			upstreamConn := &openAIWSCaptureConn{
				readDelays: []time.Duration{0, time.Hour},
				events: [][]byte{
					[]byte(`{"type":"response.completed","response":{"id":"resp_cancel_turn_1","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`),
					[]byte(`{"type":"response.output_text.delta","delta":"must-not-arrive"}`),
				},
			}
			dialer := &openAIWSCaptureDialer{conn: upstreamConn}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(dialer)
			t.Cleanup(pool.Close)

			svc := &OpenAIGatewayService{
				cfg:                       cfg,
				httpUpstream:              &httpUpstreamRecorder{},
				cache:                     &stubGatewayCache{},
				openaiWSResolver:          NewOpenAIWSProtocolResolver(cfg),
				toolCorrector:             NewCodexToolCorrector(),
				openaiWSPool:              pool,
				openaiWSPassthroughDialer: dialer,
			}

			account := &Account{
				ID:          954,
				Name:        "openai-ingress-context-cancel",
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Status:      StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test"},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_mode": mode,
				},
			}

			proxyCancelCh := make(chan context.CancelFunc, 1)
			proxyDoneCh := make(chan error, 1)
			wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{
					CompressionMode: coderws.CompressionContextTakeover,
				})
				if err != nil {
					proxyDoneCh <- err
					return
				}
				defer func() {
					_ = conn.CloseNow()
				}()

				proxyCtx, cancelProxy := context.WithCancel(r.Context())
				defer cancelProxy()
				proxyCancelCh <- cancelProxy

				rec := httptest.NewRecorder()
				ginCtx, _ := gin.CreateTestContext(rec)
				req := r.Clone(r.Context())
				req.Header = req.Header.Clone()
				req.Header.Set("User-Agent", "unit-test-agent/1.0")
				ginCtx.Request = req

				readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
				msgType, firstMessage, readErr := conn.Read(readCtx)
				cancelRead()
				if readErr != nil {
					proxyDoneCh <- readErr
					return
				}
				if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
					proxyDoneCh <- errors.New("unsupported websocket client message type")
					return
				}

				proxyDoneCh <- svc.ProxyResponsesWebSocketFromClient(
					proxyCtx,
					ginCtx,
					conn,
					account,
					"sk-test",
					firstMessage,
					nil,
				)
			}))
			defer wsServer.Close()

			dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
			clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
			cancelDial()
			require.NoError(t, err)
			defer func() {
				_ = clientConn.CloseNow()
			}()

			writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
			err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","stream":false}`))
			cancelWrite()
			require.NoError(t, err)

			readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
			_, event, err := clientConn.Read(readCtx)
			cancelRead()
			require.NoError(t, err)
			require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())

			select {
			case cancelProxy := <-proxyCancelCh:
				cancelProxy()
			case <-time.After(2 * time.Second):
				t.Fatal("等待 websocket proxy cancel 函数超时")
			}

			select {
			case <-proxyDoneCh:
				// The proxy may normalize a canceled downstream read to nil. Completion is
				// the contract under test; the close status is intentionally unspecified.
			case <-time.After(2 * time.Second):
				t.Fatal("context 取消后 websocket proxy 未结束")
			}

			clientReadCtx, cancelClientRead := context.WithTimeout(context.Background(), 2*time.Second)
			_, _, readErr := clientConn.Read(clientReadCtx)
			cancelClientRead()
			require.Error(t, readErr)
			require.NotErrorIs(t, readErr, context.DeadlineExceeded, "客户端应因服务端断链返回，而不是等到读取超时")
		})
	}
}
