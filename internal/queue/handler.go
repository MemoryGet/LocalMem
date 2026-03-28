package queue

import (
	"context"
	"encoding/json"
)

// TaskHandler 任务处理接口 / Task handler interface
type TaskHandler interface {
	Handle(ctx context.Context, payload json.RawMessage) error
}
