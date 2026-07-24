package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newGatewayRoutesTestRouter() *gin.Engine {
	return newGatewayRoutesTestRouterForPlatform(service.PlatformOpenAI)
}

func newGatewayRoutesTestRouterForPlatform(platform string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	RegisterGatewayRoutes(
		router,
		&handler.Handlers{
			Gateway:       &handler.GatewayHandler{},
			OpenAIGateway: &handler.OpenAIGatewayHandler{},
		},
		servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
			groupID := int64(1)
			c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
				GroupID: &groupID,
				Group:   &service.Group{Platform: platform},
			})
			c.Next()
		}),
		nil,
		nil,
		nil,
		nil,
		&config.Config{},
	)

	return router
}

func TestGatewayRoutesOpenAIEmbeddingsPathsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{
		"/v1/embeddings",
		"/v1/embedding",
		"/embeddings",
		"/embedding",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"text-embedding-3-large","input":"test"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit OpenAI embeddings handler", path)
	}
}

func TestGatewayRoutesEmbeddingsPathsRejectNonOpenAIPlatforms(t *testing.T) {
	router := newGatewayRoutesTestRouterForPlatform(service.PlatformAnthropic)

	for _, path := range []string{
		"/v1/embeddings",
		"/v1/embedding",
		"/embeddings",
		"/embedding",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"text-embedding-3-large","input":"test"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code, "path=%s should reject non-OpenAI groups", path)
		require.Contains(t, w.Body.String(), "Embeddings API is not supported for this platform")
	}
}

func TestGatewayRoutesOpenAIResponsesCompactPathIsRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{
		"/v1/responses/compact",
		"/responses/compact",
		"/backend-api/codex/responses",
		"/backend-api/codex/responses/compact",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-5"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit OpenAI responses handler", path)
	}
}

func TestGatewayRoutesOpenAIImagesPathsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{
		"/v1/images/generations",
		"/v1/images/edits",
		"/images/generations",
		"/images/edits",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-image-2","prompt":"draw a cat"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit OpenAI images handler", path)
	}
}

func TestGatewayRoutesOpenAIAudioTranscriptionsPathsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{
		"/v1/audio/transcriptions",
		"/audio/transcriptions",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("not-a-valid-multipart-body"))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit OpenAI audio transcriptions handler", path)
	}
}

func TestGatewayRoutesRealtimeVoiceTokenPathsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{
		"/v1/realtime/voice_token",
		"/realtime/voice_token",
		"/backend-api/voice_token",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			req := httptest.NewRequest(method, path, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)
			require.NotEqual(t, http.StatusNotFound, w.Code, "method=%s path=%s should hit realtime voice token handler", method, path)
		}
	}
}

func TestGatewayRoutesRealtimeVoiceTokenRejectsNonOpenAIPlatforms(t *testing.T) {
	router := newGatewayRoutesTestRouterForPlatform(service.PlatformAnthropic)
	req := httptest.NewRequest(http.MethodGet, "/v1/realtime/voice_token", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "Realtime voice is not supported for this platform")
}

func TestGatewayRoutesRealtimeVoiceCallPathsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{
		"/v1/realtime/calls",
		"/v1/realtime/vp",
		"/v1/realtime/vps",
		"/v1/realtime/wm",
		"/realtime/calls",
		"/realtime/vp",
		"/realtime/vps",
		"/realtime/wm",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("not-a-valid-multipart-body"))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit realtime voice call handler", path)
	}
}

func TestGatewayRoutesRealtimeVoiceCallRejectsNonOpenAIPlatforms(t *testing.T) {
	router := newGatewayRoutesTestRouterForPlatform(service.PlatformAnthropic)
	req := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "Realtime voice is not supported for this platform")
}
