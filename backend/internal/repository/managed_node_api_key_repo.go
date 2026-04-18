package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type managedNodeAPIKeyRepository struct {
	db *sql.DB
}

func NewManagedNodeAPIKeyRepository(sqlDB *sql.DB) service.ManagedNodeAPIKeyRepository {
	return &managedNodeAPIKeyRepository{db: sqlDB}
}

func (r *managedNodeAPIKeyRepository) Create(ctx context.Context, key *service.ManagedNodeAPIKey, audit *service.ManagedNodeAPIKeyAudit) error {
	if r == nil || r.db == nil {
		return infraerrors.InternalServer("MANAGED_NODE_API_KEY_DB_MISSING", "managed node API key database is unavailable")
	}
	if key == nil {
		return infraerrors.BadRequest("MANAGED_NODE_API_KEY_REQUIRED", "managed node API key payload is required")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin managed node api key create tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	query := `
		INSERT INTO managed_node_api_keys (
			name, description, key_hash, key_prefix, key_suffix, status, created_by, last_used_ip
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at
	`

	var createdBy any
	if key.CreatedBy != nil {
		createdBy = *key.CreatedBy
	}
	if err := tx.QueryRowContext(
		ctx,
		query,
		key.Name,
		key.Description,
		key.KeyHash,
		key.KeyPrefix,
		key.KeySuffix,
		key.Status,
		createdBy,
		key.LastUsedIP,
	).Scan(&key.ID, &key.CreatedAt, &key.UpdatedAt); err != nil {
		return fmt.Errorf("insert managed node api key: %w", err)
	}

	if audit != nil {
		audit.ManagedNodeAPIKeyID = key.ID
		if err := insertManagedNodeAPIKeyAudit(ctx, tx, audit); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit managed node api key create tx: %w", err)
	}
	return nil
}

func (r *managedNodeAPIKeyRepository) List(ctx context.Context) ([]service.ManagedNodeAPIKey, error) {
	if r == nil || r.db == nil {
		return []service.ManagedNodeAPIKey{}, nil
	}

	query := `
		SELECT id, name, description, key_hash, key_prefix, key_suffix, status,
			created_by, revoked_by, last_used_at, last_used_ip, created_at, updated_at, revoked_at
		FROM managed_node_api_keys
		ORDER BY created_at DESC, id DESC
	`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list managed node api keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	keys := make([]service.ManagedNodeAPIKey, 0)
	for rows.Next() {
		item, err := scanManagedNodeAPIKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate managed node api keys: %w", err)
	}
	return keys, nil
}

func (r *managedNodeAPIKeyRepository) AuthenticateActiveByHash(ctx context.Context, keyHash, ip string, usedAt time.Time, audit *service.ManagedNodeAPIKeyAudit) (*service.ManagedNodeAPIKey, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin managed node api key authenticate tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	query := `
		UPDATE managed_node_api_keys
		SET last_used_at = $2,
			last_used_ip = $3,
			updated_at = $2
		WHERE key_hash = $1
			AND status = $4
		RETURNING id, name, description, key_hash, key_prefix, key_suffix, status,
			created_by, revoked_by, last_used_at, last_used_ip, created_at, updated_at, revoked_at
	`

	var item service.ManagedNodeAPIKey
	var createdBy sql.NullInt64
	var revokedBy sql.NullInt64
	var lastUsedAt sql.NullTime
	var revokedAt sql.NullTime
	if err := scanSingleRow(
		ctx,
		tx,
		query,
		[]any{keyHash, usedAt, ip, service.ManagedNodeAPIKeyStatusActive},
		&item.ID,
		&item.Name,
		&item.Description,
		&item.KeyHash,
		&item.KeyPrefix,
		&item.KeySuffix,
		&item.Status,
		&createdBy,
		&revokedBy,
		&lastUsedAt,
		&item.LastUsedIP,
		&item.CreatedAt,
		&item.UpdatedAt,
		&revokedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("authenticate managed node api key: %w", err)
	}

	if createdBy.Valid {
		v := createdBy.Int64
		item.CreatedBy = &v
	}
	if revokedBy.Valid {
		v := revokedBy.Int64
		item.RevokedBy = &v
	}
	if lastUsedAt.Valid {
		item.LastUsedAt = &lastUsedAt.Time
	}
	if revokedAt.Valid {
		item.RevokedAt = &revokedAt.Time
	}

	if audit != nil {
		audit.ManagedNodeAPIKeyID = item.ID
		if err := insertManagedNodeAPIKeyAudit(ctx, tx, audit); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit managed node api key authenticate tx: %w", err)
	}

	return &item, nil
}

func (r *managedNodeAPIKeyRepository) TouchUsageByID(ctx context.Context, keyID int64, ip string, usedAt time.Time, audit *service.ManagedNodeAPIKeyAudit) error {
	if r == nil || r.db == nil {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin managed node api key usage tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE managed_node_api_keys
		 SET last_used_at = $2,
		     last_used_ip = $3,
		     updated_at = NOW()
		 WHERE id = $1 AND status = $4`,
		keyID,
		usedAt,
		ip,
		service.ManagedNodeAPIKeyStatusActive,
	); err != nil {
		return fmt.Errorf("touch managed node api key usage: %w", err)
	}

	if audit != nil {
		audit.ManagedNodeAPIKeyID = keyID
		if err := insertManagedNodeAPIKeyAudit(ctx, tx, audit); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit managed node api key usage tx: %w", err)
	}
	return nil
}

func (r *managedNodeAPIKeyRepository) Revoke(ctx context.Context, keyID int64, revokedBy *int64, revokedAt time.Time, audit *service.ManagedNodeAPIKeyAudit) (*service.ManagedNodeAPIKey, error) {
	if r == nil || r.db == nil {
		return nil, infraerrors.InternalServer("MANAGED_NODE_API_KEY_DB_MISSING", "managed node API key database is unavailable")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin managed node api key revoke tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var currentStatus string
	if err := tx.QueryRowContext(ctx, "SELECT status FROM managed_node_api_keys WHERE id = $1 FOR UPDATE", keyID).Scan(&currentStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.NotFound("MANAGED_NODE_API_KEY_NOT_FOUND", "managed node API key not found")
		}
		return nil, fmt.Errorf("query managed node api key before revoke: %w", err)
	}
	if currentStatus != service.ManagedNodeAPIKeyStatusActive {
		return nil, infraerrors.Conflict("MANAGED_NODE_API_KEY_ALREADY_REVOKED", "managed node API key has already been revoked")
	}

	query := `
		UPDATE managed_node_api_keys
		SET status = $2,
			revoked_by = $3,
			revoked_at = $4,
			updated_at = $4
		WHERE id = $1
		RETURNING id, name, description, key_hash, key_prefix, key_suffix, status,
			created_by, revoked_by, last_used_at, last_used_ip, created_at, updated_at, revoked_at
	`

	var item service.ManagedNodeAPIKey
	var createdBy sql.NullInt64
	var dbRevokedBy sql.NullInt64
	var lastUsedAt sql.NullTime
	var dbRevokedAt sql.NullTime
	var revokedByArg any
	if revokedBy != nil {
		revokedByArg = *revokedBy
	}
	if err := tx.QueryRowContext(
		ctx,
		query,
		keyID,
		service.ManagedNodeAPIKeyStatusRevoked,
		revokedByArg,
		revokedAt,
	).Scan(
		&item.ID,
		&item.Name,
		&item.Description,
		&item.KeyHash,
		&item.KeyPrefix,
		&item.KeySuffix,
		&item.Status,
		&createdBy,
		&dbRevokedBy,
		&lastUsedAt,
		&item.LastUsedIP,
		&item.CreatedAt,
		&item.UpdatedAt,
		&dbRevokedAt,
	); err != nil {
		return nil, fmt.Errorf("revoke managed node api key: %w", err)
	}

	if createdBy.Valid {
		v := createdBy.Int64
		item.CreatedBy = &v
	}
	if dbRevokedBy.Valid {
		v := dbRevokedBy.Int64
		item.RevokedBy = &v
	}
	if lastUsedAt.Valid {
		item.LastUsedAt = &lastUsedAt.Time
	}
	if dbRevokedAt.Valid {
		item.RevokedAt = &dbRevokedAt.Time
	}

	if audit != nil {
		audit.ManagedNodeAPIKeyID = item.ID
		if err := insertManagedNodeAPIKeyAudit(ctx, tx, audit); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit managed node api key revoke tx: %w", err)
	}
	return &item, nil
}

func (r *managedNodeAPIKeyRepository) ListAudits(ctx context.Context, keyID int64, limit int) ([]service.ManagedNodeAPIKeyAudit, error) {
	if r == nil || r.db == nil {
		return []service.ManagedNodeAPIKeyAudit{}, nil
	}
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT id, managed_node_api_key_id, action, operator_user_id, operator_role, auth_method, detail, created_at
		FROM managed_node_api_key_audits
		WHERE managed_node_api_key_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`

	rows, err := r.db.QueryContext(ctx, query, keyID, limit)
	if err != nil {
		return nil, fmt.Errorf("list managed node api key audits: %w", err)
	}
	defer func() { _ = rows.Close() }()

	audits := make([]service.ManagedNodeAPIKeyAudit, 0)
	for rows.Next() {
		var audit service.ManagedNodeAPIKeyAudit
		var operatorUserID sql.NullInt64
		var detailJSON []byte
		if err := rows.Scan(
			&audit.ID,
			&audit.ManagedNodeAPIKeyID,
			&audit.Action,
			&operatorUserID,
			&audit.OperatorRole,
			&audit.AuthMethod,
			&detailJSON,
			&audit.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan managed node api key audit: %w", err)
		}
		if operatorUserID.Valid {
			v := operatorUserID.Int64
			audit.OperatorUserID = &v
		}
		if len(detailJSON) > 0 {
			if err := json.Unmarshal(detailJSON, &audit.Detail); err != nil {
				return nil, fmt.Errorf("parse managed node api key audit detail: %w", err)
			}
		} else {
			audit.Detail = map[string]any{}
		}
		audits = append(audits, audit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate managed node api key audits: %w", err)
	}
	return audits, nil
}

func insertManagedNodeAPIKeyAudit(ctx context.Context, tx *sql.Tx, audit *service.ManagedNodeAPIKeyAudit) error {
	if audit == nil {
		return nil
	}

	detail := audit.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("marshal managed node api key audit detail: %w", err)
	}

	var operatorUserID any
	if audit.OperatorUserID != nil {
		operatorUserID = *audit.OperatorUserID
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO managed_node_api_key_audits
			(managed_node_api_key_id, action, operator_user_id, operator_role, auth_method, detail)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		audit.ManagedNodeAPIKeyID,
		strings.TrimSpace(audit.Action),
		operatorUserID,
		strings.TrimSpace(audit.OperatorRole),
		strings.TrimSpace(audit.AuthMethod),
		detailJSON,
	); err != nil {
		return fmt.Errorf("insert managed node api key audit: %w", err)
	}
	return nil
}

func scanManagedNodeAPIKey(rows *sql.Rows) (service.ManagedNodeAPIKey, error) {
	var item service.ManagedNodeAPIKey
	var createdBy sql.NullInt64
	var revokedBy sql.NullInt64
	var lastUsedAt sql.NullTime
	var revokedAt sql.NullTime

	if err := rows.Scan(
		&item.ID,
		&item.Name,
		&item.Description,
		&item.KeyHash,
		&item.KeyPrefix,
		&item.KeySuffix,
		&item.Status,
		&createdBy,
		&revokedBy,
		&lastUsedAt,
		&item.LastUsedIP,
		&item.CreatedAt,
		&item.UpdatedAt,
		&revokedAt,
	); err != nil {
		return service.ManagedNodeAPIKey{}, fmt.Errorf("scan managed node api key: %w", err)
	}

	if createdBy.Valid {
		v := createdBy.Int64
		item.CreatedBy = &v
	}
	if revokedBy.Valid {
		v := revokedBy.Int64
		item.RevokedBy = &v
	}
	if lastUsedAt.Valid {
		item.LastUsedAt = &lastUsedAt.Time
	}
	if revokedAt.Valid {
		item.RevokedAt = &revokedAt.Time
	}

	return item, nil
}
