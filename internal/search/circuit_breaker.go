package search

import (
	"sync"
	"time"
)

// circuitState 熔断器状态 / Circuit breaker state
type circuitState int

const (
	circuitClosed   circuitState = iota // 正常通行 / Normal pass-through
	circuitOpen                         // 熔断开启，跳过远程调用 / Open, skip remote calls
	circuitHalfOpen                     // 半开，允许一次试探 / Half-open, allow one probe
)

// circuitBreaker 简易熔断器，连续失败达阈值后暂停远程调用 / Simple circuit breaker that pauses remote calls after consecutive failures
type circuitBreaker struct {
	mu               sync.Mutex
	state            circuitState
	consecutiveFails int
	failThreshold    int           // 连续失败几次触发熔断 / Consecutive failures to trip
	cooldown         time.Duration // 熔断冷却时间 / Cooldown before half-open
	openedAt         time.Time     // 熔断开启时间 / When circuit opened
}

// newCircuitBreaker 创建熔断器 / Create circuit breaker
func newCircuitBreaker(failThreshold int, cooldown time.Duration) *circuitBreaker {
	if failThreshold <= 0 {
		failThreshold = 3
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &circuitBreaker{
		failThreshold: failThreshold,
		cooldown:      cooldown,
	}
}

// allow 判断是否允许请求通过 / Check if request is allowed
func (cb *circuitBreaker) allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case circuitClosed:
		return true
	case circuitOpen:
		if time.Since(cb.openedAt) >= cb.cooldown {
			cb.state = circuitHalfOpen
			return true
		}
		return false
	case circuitHalfOpen:
		return true
	default:
		return true
	}
}

// recordSuccess 记录成功 / Record success
func (cb *circuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFails = 0
	cb.state = circuitClosed
}

// recordFailure 记录失败，可能触发熔断 / Record failure, may trip breaker
func (cb *circuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFails++

	if cb.state == circuitHalfOpen {
		// 半开状态下失败直接重新熔断 / Failure in half-open re-opens circuit
		cb.state = circuitOpen
		cb.openedAt = time.Now()
		return
	}

	if cb.consecutiveFails >= cb.failThreshold {
		cb.state = circuitOpen
		cb.openedAt = time.Now()
	}
}
