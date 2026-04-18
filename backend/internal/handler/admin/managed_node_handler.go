package admin

import (
	"context"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type managedNodeResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Scheme      string `json:"scheme"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	MaskedKey   string `json:"masked_key"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type managedNodeCreateRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Scheme      string `json:"scheme"`
	Host        string `json:"host" binding:"required"`
	Port        int    `json:"port" binding:"required"`
	APIKey      string `json:"api_key" binding:"required"`
}

type managedNodeUpdateRequest struct {
	Name        string  `json:"name" binding:"required"`
	Description string  `json:"description"`
	Scheme      string  `json:"scheme"`
	Host        string  `json:"host" binding:"required"`
	Port        int     `json:"port" binding:"required"`
	APIKey      *string `json:"api_key,omitempty"`
}

type managedNodeRemoteInfoResponse struct {
	SiteName            string `json:"site_name"`
	AuthMethod          string `json:"auth_method,omitempty"`
	FrontendURL         string `json:"frontend_url,omitempty"`
	ManagedNodeAPIKeyID int64  `json:"managed_node_api_key_id,omitempty"`
}

type managedNodeJumpLinkRequest struct {
	Redirect string `json:"redirect"`
}

func managedNodeResponseFromService(item service.ManagedNode) managedNodeResponse {
	return managedNodeResponse{
		ID:          item.ID,
		Name:        item.Name,
		Description: item.Description,
		Scheme:      item.Scheme,
		Host:        item.Host,
		Port:        item.Port,
		MaskedKey:   item.MaskedKey(),
		CreatedAt:   item.CreatedAt.UTC().Format(timeLayoutRFC3339),
		UpdatedAt:   item.UpdatedAt.UTC().Format(timeLayoutRFC3339),
	}
}

func requestBaseURL(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	scheme := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto"))
	if scheme == "" {
		if c.Request.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(c.GetHeader("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(c.Request.Host)
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

func requireManagedNodeAuth(c *gin.Context) bool {
	return strings.TrimSpace(c.GetString("auth_method")) == "managed_node_api_key"
}

// ListManagedNodes 获取受管节点列表
// GET /api/v1/admin/settings/managed-nodes
func (h *SettingHandler) ListManagedNodes(c *gin.Context) {
	if h.managedNodeService == nil {
		response.InternalError(c, "Managed node service unavailable")
		return
	}
	nodes, err := h.managedNodeService.ListManagedNodes(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items := make([]managedNodeResponse, 0, len(nodes))
	for _, item := range nodes {
		items = append(items, managedNodeResponseFromService(item))
	}
	response.Success(c, items)
}

// CreateManagedNode 创建受管节点
// POST /api/v1/admin/settings/managed-nodes
func (h *SettingHandler) CreateManagedNode(c *gin.Context) {
	if h.managedNodeService == nil {
		response.InternalError(c, "Managed node service unavailable")
		return
	}
	var req managedNodeCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := h.managedNodeService.CreateManagedNode(c.Request.Context(), service.ManagedNodeCreateInput{
		Name:        req.Name,
		Description: req.Description,
		Scheme:      req.Scheme,
		Host:        req.Host,
		Port:        req.Port,
		APIKey:      req.APIKey,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, managedNodeResponseFromService(*item))
}

// UpdateManagedNode 更新受管节点
// PUT /api/v1/admin/settings/managed-nodes/:id
func (h *SettingHandler) UpdateManagedNode(c *gin.Context) {
	if h.managedNodeService == nil {
		response.InternalError(c, "Managed node service unavailable")
		return
	}
	var req managedNodeUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := h.managedNodeService.UpdateManagedNode(c.Request.Context(), c.Param("id"), service.ManagedNodeUpdateInput{
		Name:        req.Name,
		Description: req.Description,
		Scheme:      req.Scheme,
		Host:        req.Host,
		Port:        req.Port,
		APIKey:      req.APIKey,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, managedNodeResponseFromService(*item))
}

// DeleteManagedNode 删除受管节点
// DELETE /api/v1/admin/settings/managed-nodes/:id
func (h *SettingHandler) DeleteManagedNode(c *gin.Context) {
	if h.managedNodeService == nil {
		response.InternalError(c, "Managed node service unavailable")
		return
	}
	if err := h.managedNodeService.DeleteManagedNode(c.Request.Context(), c.Param("id")); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "managed node deleted"})
}

// TestManagedNode 测试受管节点连接
// POST /api/v1/admin/settings/managed-nodes/:id/test
func (h *SettingHandler) TestManagedNode(c *gin.Context) {
	if h.managedNodeService == nil {
		response.InternalError(c, "Managed node service unavailable")
		return
	}
	node, err := h.managedNodeService.GetManagedNode(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	info, err := h.managedNodeService.GetRemoteNodeInfo(c.Request.Context(), node)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, managedNodeRemoteInfoResponse{
		SiteName:            info.SiteName,
		AuthMethod:          info.AuthMethod,
		FrontendURL:         info.FrontendURL,
		ManagedNodeAPIKeyID: info.ManagedKeyID,
	})
}

// CreateManagedNodeJumpLink 为受管节点生成免密跳转链接
// POST /api/v1/admin/settings/managed-nodes/:id/jump-link
func (h *SettingHandler) CreateManagedNodeJumpLink(c *gin.Context) {
	if h.managedNodeService == nil {
		response.InternalError(c, "Managed node service unavailable")
		return
	}
	node, err := h.managedNodeService.GetManagedNode(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var req managedNodeJumpLinkRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			response.BadRequest(c, "Invalid request: "+err.Error())
			return
		}
	}
	link, err := h.managedNodeService.RequestRemoteJumpLink(c.Request.Context(), node, req.Redirect)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, link)
}

// GetFederationInfo 返回当前节点的联邦信息
// GET /api/v1/admin/settings/federation/info
func (h *SettingHandler) GetFederationInfo(c *gin.Context) {
	if !requireManagedNodeAuth(c) {
		response.Forbidden(c, "Managed node key is required")
		return
	}
	siteName := "Sub2API"
	if h.settingService != nil {
		siteName = h.settingService.GetSiteName(c.Request.Context())
	}
	var managedNodeAPIKeyID int64
	if value, exists := c.Get("managed_node_api_key_id"); exists {
		switch v := value.(type) {
		case int64:
			managedNodeAPIKeyID = v
		case int:
			managedNodeAPIKeyID = int64(v)
		}
	}
	response.Success(c, managedNodeRemoteInfoResponse{
		SiteName:            siteName,
		AuthMethod:          strings.TrimSpace(c.GetString("auth_method")),
		FrontendURL:         requestBaseURL(c),
		ManagedNodeAPIKeyID: managedNodeAPIKeyID,
	})
}

// CreateFederationSessionLink 生成当前节点的管理员免密登录链接
// POST /api/v1/admin/settings/federation/session-link
func (h *SettingHandler) CreateFederationSessionLink(c *gin.Context) {
	if h.managedNodeService == nil {
		response.InternalError(c, "Managed node service unavailable")
		return
	}
	if !requireManagedNodeAuth(c) {
		response.Forbidden(c, "Managed node key is required")
		return
	}
	var req managedNodeJumpLinkRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			response.BadRequest(c, "Invalid request: "+err.Error())
			return
		}
	}
	ctx := context.WithValue(c.Request.Context(), service.ManagedNodeContextAuthMethodKey(), strings.TrimSpace(c.GetString("auth_method")))
	if value, exists := c.Get("managed_node_api_key_id"); exists {
		switch v := value.(type) {
		case int64:
			ctx = context.WithValue(ctx, service.ManagedNodeContextKeyIDKey(), v)
		case int:
			ctx = context.WithValue(ctx, service.ManagedNodeContextKeyIDKey(), int64(v))
		}
	}
	if name := strings.TrimSpace(c.GetString("managed_node_api_key_name")); name != "" {
		ctx = context.WithValue(ctx, service.ManagedNodeContextKeyNameKey(), name)
	}
	link, err := h.managedNodeService.CreateLocalJumpLink(ctx, requestBaseURL(c), req.Redirect)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, link)
}

func parsePositiveIntOrZero(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return 0
	}
	return value
}
