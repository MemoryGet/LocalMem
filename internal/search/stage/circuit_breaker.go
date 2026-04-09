package stage

import (
	"sync"
	"time"
)

// stageCircuitBreaker 简易熔断器 / Simple circuit breaker for stages
type stageCircuitBreaker struct {
	mu               sync.Mutex
	state            int // 0=closed, 1=open, 2=half-open
	consecutiveFails int
	failThreshold    int
	cooldown         time.Duration
	openedAt         time.Time
}

func newStageCircuitBreaker(failThreshold int, cooldown time.Duration) *stageCircuitBreaker {
	if failThreshold <= 0 {
		failThreshold = 3
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &stageCircuitBreaker{
		failThreshold: failThreshold,
		cooldown:      cooldown,
	}
}

func (cb *stageCircuitBreaker) allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case 0: // closed
		return true
	case 1: // open
		if time.Since(cb.openedAt) >= cb.cooldown {
			cb.state = 2 // half-open
			return true
		}
		return false
	case 2: // half-open
		return true
	default:
		return true
	}
}

func (cb *stageCircuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveFails = 0
	cb.state = 0
}

func (cb *stageCircuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveFails++
	if cb.state == 2 { // half-open
		cb.state = 1
		cb.openedAt = time.Now()
		return
	}
	if cb.consecutiveFails >= cb.failThreshold {
		cb.state = 1
		cb.openedAt = time.Now()
	}
}
