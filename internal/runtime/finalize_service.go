package runtime

import (
	"context"
	"fmt"
	"time"

	"iclude/internal/logger"
	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// FinalizeRequest finalize 请求参数 / Finalize request parameters
type FinalizeRequest struct {
	SessionID      string `json:"session_id"`
	ContextID      string `json:"context_id"`
	ToolName       string `json:"tool_name"`
	IdempotencyKey string `json:"idempotency_key"`
	Summary        string `json:"summary,omitempty"`   // adapter 侧预生成摘要 / Pre-generated summary from adapter
}

// FinalizeResponse finalize 响应 / Finalize response
type FinalizeResponse struct {
	Finalized             bool       `json:"finalized"`
	ConversationIngested  bool       `json:"conversation_ingested"`
	SummaryMemoryID       string     `json:"summary_memory_id,omitempty"`
	FinalizedAt           *time.Time `json:"finalized_at,omitempty"`
	AlreadyFinalized      bool       `json:"already_finalized,omitempty"` // 幂等重入 / Idempotent re-entry
}

// Summarizer 会话摘要生成接口 / Session summarizer interface (consumed by FinalizeService)
type Summarizer interface {
	SummarizeBySourceRef(ctx context.Context, sourceRef string, identity *model.Identity) (*memory.SummarizeResponse, error)
}

// FinalizeService 会话终结编排服务 / Session finalize orchestration service
type FinalizeService struct {
	sessions   store.SessionStore
	finalize   store.SessionFinalizeStore
	idempotent store.IdempotencyStore
	summarizer Summarizer // 可为 nil / may be nil
}

// NewFinalizeService 创建 finalize 服务 / Create finalize service
func NewFinalizeService(
	sessions store.SessionStore,
	finalize store.SessionFinalizeStore,
	idempotent store.IdempotencyStore,
	summarizer Summarizer,
) *FinalizeService {
	return &FinalizeService{
		sessions:   sessions,
		finalize:   finalize,
		idempotent: idempotent,
		summarizer: summarizer,
	}
}

// Finalize 执行会话终结（幂等）/ Execute session finalize (idempotent)
func (s *FinalizeService) Finalize(ctx context.Context, req *FinalizeRequest, identity *model.Identity) (*FinalizeResponse, error) {
	if req.SessionID == "" {
		return nil, fmt.Errorf("session_id is required: %w", model.ErrInvalidInput)
	}
	if req.IdempotencyKey == "" {
		return nil, fmt.Errorf("idempotency_key is required: %w", model.ErrInvalidInput)
	}

	// 1. 幂等检查 / Idempotency check
	reserved, err := s.idempotent.Reserve(ctx, "finalize", req.IdempotencyKey, "session")
	if err != nil {
		return nil, fmt.Errorf("idempotency reserve: %w", err)
	}
	if !reserved {
		// 已处理过，查现有状态返回 / Already processed, return existing state
		return s.buildIdempotentResponse(ctx, req.SessionID)
	}

	// 2. 获取 session / Get session
	sess, err := s.sessions.Get(ctx, req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	// 已经 finalized 的直接返回 / Already finalized, return directly
	if sess.State == model.SessionStateFinalized {
		return s.buildIdempotentResponse(ctx, req.SessionID)
	}

	// 3. 标记 finalizing / Mark finalizing
	if sess.State == model.SessionStateActive || sess.State == model.SessionStatePendingRepair {
		if err := s.sessions.UpdateState(ctx, req.SessionID, model.SessionStateFinalizing); err != nil {
			logger.Warn("finalize: failed to mark finalizing", zap.Error(err))
		}
	}

	resp := &FinalizeResponse{}

	// 4. 生成摘要 / Generate summary
	var summaryMemoryID string
	if s.summarizer != nil && identity != nil {
		sumResp, sumErr := s.summarizer.SummarizeBySourceRef(ctx, req.SessionID, identity)
		if sumErr != nil {
			logger.Warn("finalize: summarizer failed, continuing without summary",
				zap.String("session_id", req.SessionID),
				zap.Error(sumErr),
			)
			// 记录错误但不中止 / Log error but don't abort
			_ = s.finalize.Upsert(ctx, &model.SessionFinalizeState{
				SessionID: req.SessionID,
				LastError: fmt.Sprintf("summarizer: %v", sumErr),
			})
		} else if sumResp != nil && !sumResp.Skipped && sumResp.SemanticMemory != nil {
			summaryMemoryID = sumResp.SemanticMemory.ID
			resp.SummaryMemoryID = summaryMemoryID
			logger.Info("finalize: summary generated",
				zap.String("session_id", req.SessionID),
				zap.String("summary_memory_id", summaryMemoryID),
				zap.Int("source_count", sumResp.SourceCount),
			)
		}
	}

	// 5. 推进 finalize 状态 / Advance finalize state
	now := time.Now()
	if err := s.finalize.MarkFinalized(ctx, req.SessionID, 1, summaryMemoryID); err != nil {
		// 标记失败 → pending_repair / Mark failed → pending_repair
		_ = s.sessions.UpdateState(ctx, req.SessionID, model.SessionStatePendingRepair)
		return nil, fmt.Errorf("mark finalized: %w", err)
	}

	// 6. 标记 session finalized / Mark session finalized
	if err := s.sessions.UpdateState(ctx, req.SessionID, model.SessionStateFinalized); err != nil {
		logger.Warn("finalize: failed to mark session finalized", zap.Error(err))
	}

	// 7. 绑定幂等键到 session / Bind idempotency key to session
	_ = s.idempotent.BindResource(ctx, "finalize", req.IdempotencyKey, req.SessionID)

	resp.Finalized = true
	resp.FinalizedAt = &now
	resp.ConversationIngested = true

	logger.Info("runtime.finalize_succeeded",
		zap.String("session_id", req.SessionID),
		zap.Time("finalized_at", now),
	)

	return resp, nil
}

// buildIdempotentResponse 构建幂等重入响应 / Build idempotent re-entry response
func (s *FinalizeService) buildIdempotentResponse(ctx context.Context, sessionID string) (*FinalizeResponse, error) {
	resp := &FinalizeResponse{AlreadyFinalized: true, Finalized: true}

	st, err := s.finalize.Get(ctx, sessionID)
	if err == nil {
		resp.ConversationIngested = st.ConversationIngested
		resp.SummaryMemoryID = st.SummaryMemoryID
	}

	sess, err := s.sessions.Get(ctx, sessionID)
	if err == nil && sess.FinalizedAt != nil {
		resp.FinalizedAt = sess.FinalizedAt
	}

	return resp, nil
}
