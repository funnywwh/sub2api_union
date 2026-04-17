package admin

import (
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type managedNodeAPIKeyCreateRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

type managedNodeAPIKeyRevokeRequest struct {
	Reason string `json:"reason"`
}

type managedNodeAPIKeyResponse struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	MaskedKey   string  `json:"masked_key"`
	Status      string  `json:"status"`
	CreatedBy   *int64  `json:"created_by,omitempty"`
	RevokedBy   *int64  `json:"revoked_by,omitempty"`
	LastUsedAt  *string `json:"last_used_at,omitempty"`
	LastUsedIP  string  `json:"last_used_ip,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	RevokedAt   *string `json:"revoked_at,omitempty"`
}

type managedNodeAPIKeyAuditResponse struct {
	ID             int64          `json:"id"`
	Action         string         `json:"action"`
	OperatorUserID *int64         `json:"operator_user_id,omitempty"`
	OperatorRole   string         `json:"operator_role,omitempty"`
	AuthMethod     string         `json:"auth_method,omitempty"`
	Detail         map[string]any `json:"detail"`
	CreatedAt      string         `json:"created_at"`
}

// ListManagedNodeAPIKeys 获取受管节点 API Keys 列表
// GET /api/v1/admin/settings/managed-node-keys
func (h *SettingHandler) ListManagedNodeAPIKeys(c *gin.Context) {
	if h.managedNodeAPIKeyService == nil {
		response.InternalError(c, "Managed node API key service unavailable")
		return
	}
	keys, err := h.managedNodeAPIKeyService.ListKeys(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	items := make([]managedNodeAPIKeyResponse, 0, len(keys))
	for i := range keys {
		items = append(items, managedNodeAPIKeyResponseFromService(keys[i]))
	}
	response.Success(c, items)
}

// CreateManagedNodeAPIKey 创建新的受管节点 API Key
// POST /api/v1/admin/settings/managed-node-keys
func (h *SettingHandler) CreateManagedNodeAPIKey(c *gin.Context) {
	if h.managedNodeAPIKeyService == nil {
		response.InternalError(c, "Managed node API key service unavailable")
		return
	}
	var req managedNodeAPIKeyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	operatorUserID, operatorRole, authMethod, operatorManagedNodeKeyID, operatorManagedNodeKeyName := managedNodeAPIKeyActor(c)
	key, rawKey, err := h.managedNodeAPIKeyService.CreateKey(c.Request.Context(), service.ManagedNodeAPIKeyCreateInput{
		Name:                       req.Name,
		Description:                req.Description,
		OperatorUserID:             operatorUserID,
		OperatorRole:               operatorRole,
		AuthMethod:                 authMethod,
		OperatorManagedNodeKeyID:   operatorManagedNodeKeyID,
		OperatorManagedNodeKeyName: operatorManagedNodeKeyName,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	slog.Info("managed node api key created",
		"audit", true,
		"user_id", derefInt64(operatorUserID),
		"role", operatorRole,
		"auth_method", authMethod,
		"managed_node_api_key_id", key.ID,
		"managed_node_api_key_name", key.Name,
		"operator_managed_node_api_key_id", derefInt64(operatorManagedNodeKeyID),
		"operator_managed_node_api_key_name", operatorManagedNodeKeyName,
	)

	response.Created(c, gin.H{
		"key":  rawKey,
		"item": managedNodeAPIKeyResponseFromService(*key),
	})
}

// RevokeManagedNodeAPIKey 作废受管节点 API Key
// POST /api/v1/admin/settings/managed-node-keys/:id/revoke
func (h *SettingHandler) RevokeManagedNodeAPIKey(c *gin.Context) {
	if h.managedNodeAPIKeyService == nil {
		response.InternalError(c, "Managed node API key service unavailable")
		return
	}
	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || keyID <= 0 {
		response.BadRequest(c, "Invalid managed node API key ID")
		return
	}

	var req managedNodeAPIKeyRevokeRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			response.BadRequest(c, "Invalid request: "+err.Error())
			return
		}
	}

	operatorUserID, operatorRole, authMethod, operatorManagedNodeKeyID, operatorManagedNodeKeyName := managedNodeAPIKeyActor(c)
	key, err := h.managedNodeAPIKeyService.RevokeKey(c.Request.Context(), keyID, service.ManagedNodeAPIKeyRevokeInput{
		Reason:                     req.Reason,
		OperatorUserID:             operatorUserID,
		OperatorRole:               operatorRole,
		AuthMethod:                 authMethod,
		OperatorManagedNodeKeyID:   operatorManagedNodeKeyID,
		OperatorManagedNodeKeyName: operatorManagedNodeKeyName,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	slog.Info("managed node api key revoked",
		"audit", true,
		"user_id", derefInt64(operatorUserID),
		"role", operatorRole,
		"auth_method", authMethod,
		"managed_node_api_key_id", key.ID,
		"managed_node_api_key_name", key.Name,
		"reason", strings.TrimSpace(req.Reason),
		"operator_managed_node_api_key_id", derefInt64(operatorManagedNodeKeyID),
		"operator_managed_node_api_key_name", operatorManagedNodeKeyName,
	)

	response.Success(c, managedNodeAPIKeyResponseFromService(*key))
}

// ListManagedNodeAPIKeyAudits 获取受管节点 API Key 审计记录
// GET /api/v1/admin/settings/managed-node-keys/:id/audits
func (h *SettingHandler) ListManagedNodeAPIKeyAudits(c *gin.Context) {
	if h.managedNodeAPIKeyService == nil {
		response.InternalError(c, "Managed node API key service unavailable")
		return
	}
	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || keyID <= 0 {
		response.BadRequest(c, "Invalid managed node API key ID")
		return
	}

	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	audits, err := h.managedNodeAPIKeyService.ListAudits(c.Request.Context(), keyID, limit)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	items := make([]managedNodeAPIKeyAuditResponse, 0, len(audits))
	for i := range audits {
		items = append(items, managedNodeAPIKeyAuditResponseFromService(audits[i]))
	}
	response.Success(c, items)
}

func managedNodeAPIKeyActor(c *gin.Context) (*int64, string, string, *int64, string) {
	var operatorUserID *int64
	if subject, ok := middleware.GetAuthSubjectFromContext(c); ok && subject.UserID > 0 {
		v := subject.UserID
		operatorUserID = &v
	}
	role, _ := middleware.GetUserRoleFromContext(c)
	var operatorManagedNodeKeyID *int64
	if value, exists := c.Get("managed_node_api_key_id"); exists {
		switch v := value.(type) {
		case int64:
			operatorManagedNodeKeyID = &v
		case int:
			vv := int64(v)
			operatorManagedNodeKeyID = &vv
		}
	}
	operatorManagedNodeKeyName := strings.TrimSpace(c.GetString("managed_node_api_key_name"))
	return operatorUserID, role, strings.TrimSpace(c.GetString("auth_method")), operatorManagedNodeKeyID, operatorManagedNodeKeyName
}

func managedNodeAPIKeyResponseFromService(item service.ManagedNodeAPIKey) managedNodeAPIKeyResponse {
	out := managedNodeAPIKeyResponse{
		ID:          item.ID,
		Name:        item.Name,
		Description: item.Description,
		MaskedKey:   item.MaskedKey(),
		Status:      item.Status,
		CreatedBy:   item.CreatedBy,
		RevokedBy:   item.RevokedBy,
		LastUsedIP:  item.LastUsedIP,
		CreatedAt:   item.CreatedAt.UTC().Format(timeLayoutRFC3339),
		UpdatedAt:   item.UpdatedAt.UTC().Format(timeLayoutRFC3339),
	}
	if item.LastUsedAt != nil {
		v := item.LastUsedAt.UTC().Format(timeLayoutRFC3339)
		out.LastUsedAt = &v
	}
	if item.RevokedAt != nil {
		v := item.RevokedAt.UTC().Format(timeLayoutRFC3339)
		out.RevokedAt = &v
	}
	return out
}

func managedNodeAPIKeyAuditResponseFromService(item service.ManagedNodeAPIKeyAudit) managedNodeAPIKeyAuditResponse {
	detail := item.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	return managedNodeAPIKeyAuditResponse{
		ID:             item.ID,
		Action:         item.Action,
		OperatorUserID: item.OperatorUserID,
		OperatorRole:   item.OperatorRole,
		AuthMethod:     item.AuthMethod,
		Detail:         detail,
		CreatedAt:      item.CreatedAt.UTC().Format(timeLayoutRFC3339),
	}
}

func derefInt64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

const timeLayoutRFC3339 = "2006-01-02T15:04:05Z07:00"
