package service

import (
	"context"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrHappyHorseTaskNotFound = infraerrors.NotFound("HAPPYHORSE_TASK_NOT_FOUND", "happyhorse task not found")

type HappyHorseTask struct {
	ID                 int64
	UserID             int64
	APIKeyID           int64
	AccountID          int64
	GroupID            *int64
	TaskID             string
	RequestID          string
	Model              string
	Prompt             string
	Status             string
	ResultURLs         []string
	ErrorMessage       string
	UpstreamResponse   map[string]any
	RequestPayloadHash string
	UsageRecorded      bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
	CompletedAt        *time.Time
}

type HappyHorseTaskStatusUpdate struct {
	Status           string
	ResultURLs       []string
	ErrorMessage     string
	UpstreamResponse map[string]any
	CompletedAt      *time.Time
}

type HappyHorseTaskRepository interface {
	Create(ctx context.Context, task *HappyHorseTask) error
	GetByTaskID(ctx context.Context, taskID string) (*HappyHorseTask, error)
	UpdateStatus(ctx context.Context, taskID string, update HappyHorseTaskStatusUpdate) error
	ClaimUsageRecording(ctx context.Context, taskID string) (bool, error)
	MarkUsageRecorded(ctx context.Context, taskID string) error
	ResetUsageRecording(ctx context.Context, taskID string) error
}
