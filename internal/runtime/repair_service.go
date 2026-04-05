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

// RepairConfig 修复服务配置 / Repair service configuration
type RepairConfig struct {
	StaleDuration  time.Duration // 多久未活跃视为待修复 / How long inactive before repair (default 30min)
	MaxAttempts    int           // 最大修复尝试次数 / Max repair attempts before abandon (default 3)
	BatchSize      int           // 每批处理数 / Batch size per repair run (default 10)
}

// DefaultRepairConfig 默认配置 / Default repair configuration
func DefaultRepairConfig() RepairConfig {
	return RepairConfig{
		StaleDuration: 30 * time.Minute,
		MaxAttempts:   3,
		BatchSize:     10,
	}
}

// RepairService 会话修复服务 / Session repair service
type RepairService struct {
	sessions   store.SessionStore
	finalize   *FinalizeService
	cfg        RepairConfig
}

// NewRepairService 创建修复服务 / Create repair service
func NewRepairService(sessions store.SessionStore, finalize *FinalizeService, cfg RepairConfig) *RepairService {
	return &RepairService{
		sessions: sessions,
		finalize: finalize,
		cfg:      cfg,
	}
}

// RepairResult 单次修复运行结果 / Result of a single repair run
type RepairResult struct {
	Scanned   int `json:"scanned"`
	Repaired  int `json:"repaired"`
	Abandoned int `json:"abandoned"`
	Failed    int `json:"failed"`
}

// Run 执行一轮修复扫描 / Execute one repair scan cycle
func (r *RepairService) Run(ctx context.Context) error {
	result, err := r.repair(ctx)
	if err != nil {
		return err
	}
	if result.Scanned > 0 {
		logger.Info("runtime.repair_completed",
			zap.Int("scanned", result.Scanned),
			zap.Int("repaired", result.Repaired),
			zap.Int("abandoned", result.Abandoned),
			zap.Int("failed", result.Failed),
		)
	}
	return nil
}

// repair 内部修复逻辑 / Internal repair logic
func (r *RepairService) repair(ctx context.Context) (*RepairResult, error) {
	pending, err := r.sessions.ListPendingFinalize(ctx, r.cfg.StaleDuration, r.cfg.BatchSize)
	if err != nil {
		return nil, fmt.Errorf("list pending finalize: %w", err)
	}

	result := &RepairResult{Scanned: len(pending)}

	for _, sess := range pending {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		repaired, err := r.repairSession(ctx, sess)
		if err != nil {
			logger.Warn("runtime.repair_failed",
				zap.String("session_id", sess.ID),
				zap.Error(err),
			)
			result.Failed++
			continue
		}
		if repaired {
			result.Repaired++
		} else {
			result.Abandoned++
		}
	}

	return result, nil
}

// repairSession 修复单个会话 / Repair a single session
// 返回 true=成功 finalize, false=已标记 abandoned
func (r *RepairService) repairSession(ctx context.Context, sess *model.Session) (bool, error) {
	// 1. 读取当前修复尝试次数 / Read current repair attempt count
	attempts := getRepairAttempts(sess)

	// 2. 超限检查 → abandoned / Max attempts exceeded → abandoned
	if attempts >= r.cfg.MaxAttempts {
		if err := r.sessions.UpdateState(ctx, sess.ID, model.SessionStateAbandoned); err != nil {
			return false, fmt.Errorf("mark abandoned: %w", err)
		}
		logger.Warn("runtime.repair_abandoned",
			zap.String("session_id", sess.ID),
			zap.Int("attempts", attempts),
			zap.Int("max_attempts", r.cfg.MaxAttempts),
		)
		return false, nil
	}

	// 3. 递增尝试次数 / Increment attempt count
	if err := r.sessions.UpdateMetadata(ctx, sess.ID, map[string]any{
		"repair_attempts": attempts + 1,
	}); err != nil {
		logger.Warn("runtime.repair_update_attempts_failed", zap.Error(err))
	}

	// 4. 尝试 finalize / Try finalize
	idemKey := fmt.Sprintf("finalize:%s:%s:repair_%d", sess.ToolName, sess.ID, time.Now().Unix())
	req := &FinalizeRequest{
		SessionID:      sess.ID,
		ContextID:      sess.ContextID,
		ToolName:       sess.ToolName,
		IdempotencyKey: idemKey,
	}
	identity := &model.Identity{
		OwnerID: sess.UserID,
	}

	resp, err := r.finalize.Finalize(ctx, req, identity)
	if err != nil {
		// finalize 失败 → 标记 pending_repair 等下一轮 / Failed → mark pending_repair for next cycle
		if sess.State != model.SessionStatePendingRepair {
			_ = r.sessions.UpdateState(ctx, sess.ID, model.SessionStatePendingRepair)
		}
		return false, fmt.Errorf("repair finalize: %w", err)
	}

	if resp.Finalized {
		logger.Info("runtime.repair_succeeded",
			zap.String("session_id", sess.ID),
			zap.Int("attempts", attempts+1),
		)
		return true, nil
	}

	return false, nil
}

// getRepairAttempts 从 session metadata 读取修复尝试次数 / Read repair attempts from session metadata
func getRepairAttempts(sess *model.Session) int {
	if sess.Metadata == nil {
		return 0
	}
	v, ok := sess.Metadata["repair_attempts"]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}
