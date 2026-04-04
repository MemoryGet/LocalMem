// Package runtime 会话运行时服务层 / Session runtime service layer
package runtime

import (
	"context"
	"fmt"
	"time"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// SessionService 会话生命周期管理 / Session lifecycle management
type SessionService struct {
	sessions store.SessionStore
}

// NewSessionService 创建会话服务 / Create session service
func NewSessionService(sessions store.SessionStore) *SessionService {
	return &SessionService{sessions: sessions}
}

// Create 创建新会话并标记 created / Create new session in created state
func (s *SessionService) Create(ctx context.Context, sess *model.Session) error {
	if sess.ID == "" {
		return fmt.Errorf("session_id is required: %w", model.ErrInvalidInput)
	}
	if sess.State == "" {
		sess.State = model.SessionStateCreated
	}
	now := time.Now()
	if sess.StartedAt.IsZero() {
		sess.StartedAt = now
	}
	if sess.LastSeenAt.IsZero() {
		sess.LastSeenAt = now
	}

	if err := s.sessions.Create(ctx, sess); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	logger.Info("runtime.session_created",
		zap.String("session_id", sess.ID),
		zap.String("tool_name", sess.ToolName),
		zap.String("project_id", sess.ProjectID),
	)
	return nil
}

// Get 获取会话 / Get session by ID
func (s *SessionService) Get(ctx context.Context, id string) (*model.Session, error) {
	return s.sessions.Get(ctx, id)
}

// MarkBootstrapped 标记 bootstrap 完成 / Mark session as bootstrapped
func (s *SessionService) MarkBootstrapped(ctx context.Context, id string) error {
	return s.transition(ctx, id, model.SessionStateBootstrapped)
}

// MarkActive 标记活跃 / Mark session as active
func (s *SessionService) MarkActive(ctx context.Context, id string) error {
	return s.transition(ctx, id, model.SessionStateActive)
}

// MarkFinalizing 标记正在终结 / Mark session as finalizing
func (s *SessionService) MarkFinalizing(ctx context.Context, id string) error {
	return s.transition(ctx, id, model.SessionStateFinalizing)
}

// MarkFinalized 标记已终结 / Mark session as finalized
func (s *SessionService) MarkFinalized(ctx context.Context, id string) error {
	return s.transition(ctx, id, model.SessionStateFinalized)
}

// MarkPendingRepair 标记待修复 / Mark session as pending repair
func (s *SessionService) MarkPendingRepair(ctx context.Context, id string) error {
	return s.transition(ctx, id, model.SessionStatePendingRepair)
}

// Touch 更新最后活跃时间 / Update last seen timestamp
func (s *SessionService) Touch(ctx context.Context, id string) error {
	return s.sessions.Touch(ctx, id, time.Now())
}

// ListPendingFinalize 列出待终结会话 / List sessions pending finalize
func (s *SessionService) ListPendingFinalize(ctx context.Context, olderThan time.Duration, limit int) ([]*model.Session, error) {
	return s.sessions.ListPendingFinalize(ctx, olderThan, limit)
}

// transition 状态转移（含合法性校验）/ State transition with validation
func (s *SessionService) transition(ctx context.Context, id, target string) error {
	sess, err := s.sessions.Get(ctx, id)
	if err != nil {
		return err
	}

	if !isValidTransition(sess.State, target) {
		return fmt.Errorf("invalid state transition %s → %s for session %s: %w",
			sess.State, target, id, model.ErrInvalidInput)
	}

	if err := s.sessions.UpdateState(ctx, id, target); err != nil {
		return err
	}

	logger.Debug("session state transitioned",
		zap.String("session_id", id),
		zap.String("from", sess.State),
		zap.String("to", target),
	)
	return nil
}

// isValidTransition 校验状态机合法转移 / Validate state machine transition
func isValidTransition(from, to string) bool {
	allowed := map[string][]string{
		model.SessionStateCreated:       {model.SessionStateBootstrapped},
		model.SessionStateBootstrapped:  {model.SessionStateActive},
		model.SessionStateActive:        {model.SessionStateFinalizing, model.SessionStatePendingRepair},
		model.SessionStateFinalizing:    {model.SessionStateFinalized, model.SessionStatePendingRepair},
		model.SessionStatePendingRepair: {model.SessionStateFinalizing, model.SessionStateAbandoned},
	}
	for _, t := range allowed[from] {
		if t == to {
			return true
		}
	}
	return false
}
