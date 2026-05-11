package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type happyHorseTaskRepository struct {
	db *sql.DB
}

func NewHappyHorseTaskRepository(db *sql.DB) service.HappyHorseTaskRepository {
	return &happyHorseTaskRepository{db: db}
}

func (r *happyHorseTaskRepository) Create(ctx context.Context, task *service.HappyHorseTask) error {
	if task == nil {
		return fmt.Errorf("happyhorse task is nil")
	}
	resultURLs, err := json.Marshal(task.ResultURLs)
	if err != nil {
		return fmt.Errorf("marshal result_urls: %w", err)
	}
	upstreamResponse, err := json.Marshal(task.UpstreamResponse)
	if err != nil {
		return fmt.Errorf("marshal upstream_response: %w", err)
	}
	query := `
INSERT INTO happyhorse_tasks (
    user_id, api_key_id, account_id, group_id, task_id, request_id, model, prompt,
    status, result_urls, error_message, upstream_response, request_payload_hash, usage_recorded
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
RETURNING id, created_at, updated_at`
	return r.db.QueryRowContext(ctx, query,
		task.UserID,
		task.APIKeyID,
		task.AccountID,
		task.GroupID,
		task.TaskID,
		task.RequestID,
		task.Model,
		task.Prompt,
		task.Status,
		resultURLs,
		task.ErrorMessage,
		upstreamResponse,
		task.RequestPayloadHash,
		task.UsageRecorded,
	).Scan(&task.ID, &task.CreatedAt, &task.UpdatedAt)
}

func (r *happyHorseTaskRepository) GetByTaskID(ctx context.Context, taskID string) (*service.HappyHorseTask, error) {
	query := `
SELECT id, user_id, api_key_id, account_id, group_id, task_id, request_id, model, prompt,
       status, result_urls, error_message, upstream_response, request_payload_hash, usage_recorded,
       created_at, updated_at, completed_at
FROM happyhorse_tasks
WHERE task_id = $1`
	row := r.db.QueryRowContext(ctx, query, taskID)
	return scanHappyHorseTask(row)
}

func (r *happyHorseTaskRepository) UpdateStatus(ctx context.Context, taskID string, update service.HappyHorseTaskStatusUpdate) error {
	resultURLs, err := json.Marshal(update.ResultURLs)
	if err != nil {
		return fmt.Errorf("marshal result_urls: %w", err)
	}
	upstreamResponse, err := json.Marshal(update.UpstreamResponse)
	if err != nil {
		return fmt.Errorf("marshal upstream_response: %w", err)
	}
	res, err := r.db.ExecContext(ctx, `
UPDATE happyhorse_tasks
SET status = COALESCE(NULLIF($2, ''), status),
    result_urls = $3,
    error_message = $4,
    upstream_response = $5,
    completed_at = COALESCE($6, completed_at),
    updated_at = NOW()
WHERE task_id = $1`, taskID, update.Status, resultURLs, update.ErrorMessage, upstreamResponse, update.CompletedAt)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrHappyHorseTaskNotFound
	}
	return nil
}

func (r *happyHorseTaskRepository) ClaimUsageRecording(ctx context.Context, taskID string) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
UPDATE happyhorse_tasks
SET usage_recording_started_at = NOW(),
    updated_at = NOW()
WHERE task_id = $1
  AND usage_recorded = FALSE
  AND (usage_recording_started_at IS NULL OR usage_recording_started_at < NOW() - INTERVAL '15 minutes')`, taskID)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (r *happyHorseTaskRepository) MarkUsageRecorded(ctx context.Context, taskID string) error {
	res, err := r.db.ExecContext(ctx, `
UPDATE happyhorse_tasks
SET usage_recorded = TRUE,
    usage_recording_started_at = NULL,
    usage_recorded_at = COALESCE(usage_recorded_at, NOW()),
    updated_at = NOW()
WHERE task_id = $1`, taskID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrHappyHorseTaskNotFound
	}
	return nil
}

func (r *happyHorseTaskRepository) ResetUsageRecording(ctx context.Context, taskID string) error {
	res, err := r.db.ExecContext(ctx, `
UPDATE happyhorse_tasks
SET usage_recording_started_at = NULL,
    updated_at = NOW()
WHERE task_id = $1 AND usage_recorded = FALSE`, taskID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return nil
	}
	return nil
}

type happyHorseTaskScanner interface {
	Scan(dest ...any) error
}

func scanHappyHorseTask(row happyHorseTaskScanner) (*service.HappyHorseTask, error) {
	var task service.HappyHorseTask
	var groupID sql.NullInt64
	var resultURLsRaw []byte
	var upstreamResponseRaw []byte
	var completedAt sql.NullTime
	if err := row.Scan(
		&task.ID,
		&task.UserID,
		&task.APIKeyID,
		&task.AccountID,
		&groupID,
		&task.TaskID,
		&task.RequestID,
		&task.Model,
		&task.Prompt,
		&task.Status,
		&resultURLsRaw,
		&task.ErrorMessage,
		&upstreamResponseRaw,
		&task.RequestPayloadHash,
		&task.UsageRecorded,
		&task.CreatedAt,
		&task.UpdatedAt,
		&completedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrHappyHorseTaskNotFound
		}
		return nil, err
	}
	if groupID.Valid {
		task.GroupID = &groupID.Int64
	}
	if completedAt.Valid {
		t := completedAt.Time
		task.CompletedAt = &t
	}
	if len(resultURLsRaw) > 0 {
		_ = json.Unmarshal(resultURLsRaw, &task.ResultURLs)
	}
	if len(upstreamResponseRaw) > 0 {
		_ = json.Unmarshal(upstreamResponseRaw, &task.UpstreamResponse)
	}
	if task.UpstreamResponse == nil {
		task.UpstreamResponse = map[string]any{}
	}
	if task.ResultURLs == nil {
		task.ResultURLs = []string{}
	}
	return &task, nil
}
