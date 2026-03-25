package heartbeat

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/logger"

	"go.uber.org/zap"
)

// runContradictionCheck 矛盾检测 / Contradiction detection
// 从知识图谱中找共享实体的记忆对，用 LLM 判断是否矛盾
func (e *Engine) runContradictionCheck(ctx context.Context, cfg config.HeartbeatConfig) error {
	// 获取实体列表
	entities, err := e.graphStore.ListEntities(ctx, "", "", 100)
	if err != nil {
		return err
	}

	comparisons := 0
	contradictions := 0

	for _, entity := range entities {
		if comparisons >= cfg.ContradictionMaxComp {
			break
		}

		// 获取该实体关联的记忆
		memories, err := e.graphStore.GetEntityMemories(ctx, entity.ID, 20)
		if err != nil || len(memories) < 2 {
			continue
		}

		// 获取向量用于相似度过滤
		ids := make([]string, len(memories))
		for i, m := range memories {
			ids[i] = m.ID
		}
		vectors, err := e.vecStore.GetVectors(ctx, ids)
		if err != nil {
			continue
		}

		// 对高相似度但内容不同的记忆对做 LLM 判断
		for i := 0; i < len(memories) && comparisons < cfg.ContradictionMaxComp; i++ {
			for j := i + 1; j < len(memories) && comparisons < cfg.ContradictionMaxComp; j++ {
				va := vectors[memories[i].ID]
				vb := vectors[memories[j].ID]
				if len(va) == 0 || len(vb) == 0 {
					continue
				}

				sim := cosineSim(va, vb)
				// 只检查中等相似度的对（太高=重复，太低=无关）
				if sim < 0.5 || sim > 0.95 {
					continue
				}

				comparisons++
				isContradiction, err := e.checkContradictionWithLLM(ctx, memories[i].Content, memories[j].Content)
				if err != nil {
					logger.Warn("heartbeat: LLM contradiction check failed",
						zap.String("id_a", memories[i].ID),
						zap.String("id_b", memories[j].ID),
						zap.Error(err),
					)
					continue
				}

				if isContradiction {
					contradictions++
					logger.Warn("heartbeat: contradiction detected",
						zap.String("id_a", memories[i].ID),
						zap.String("id_b", memories[j].ID),
						zap.String("entity", entity.Name),
						zap.Float64("similarity", sim),
					)
				}
			}
		}
	}

	if contradictions > 0 {
		logger.Info("heartbeat: contradiction check completed",
			zap.Int("comparisons", comparisons),
			zap.Int("contradictions", contradictions),
		)
	}
	return nil
}

// contradictionLLMTimeout 单次矛盾检测 LLM 超时 / Per-call timeout for contradiction LLM check
const contradictionLLMTimeout = 15 * time.Second

// checkContradictionWithLLM 用 LLM 判断两条记忆是否矛盾
func (e *Engine) checkContradictionWithLLM(ctx context.Context, contentA, contentB string) (bool, error) {
	prompt := fmt.Sprintf("Do these two statements contradict each other? Answer only 'yes' or 'no'.\n\nStatement A: %s\nStatement B: %s", contentA, contentB)

	// 独立超时防止单个 LLM 调用 hang 住整次巡检 / Per-call timeout prevents hanging the full inspection run
	llmCtx, cancel := context.WithTimeout(ctx, contradictionLLMTimeout)
	defer cancel()

	resp, err := e.llm.Chat(llmCtx, &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "You are a fact-checking engine. Answer only 'yes' or 'no'."},
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return false, err
	}

	answer := strings.ToLower(strings.TrimSpace(resp.Content))
	return answer == "yes", nil
}

// cosineSim 余弦相似度
func cosineSim(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	d := math.Sqrt(na) * math.Sqrt(nb)
	if d == 0 {
		return 0
	}
	return dot / d
}
