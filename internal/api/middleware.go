package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"
	"time"

	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

const identityKey = "iclude_identity"

// AuthMiddleware API Key 认证中间件 / API Key authentication middleware
func AuthMiddleware(cfg config.AuthConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cfg.Enabled {
			c.Set("team_id", "default")
			c.Next()
			return
		}

		auth := c.GetHeader("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			c.AbortWithStatusJSON(401, gin.H{"error": "authentication required"})
			return
		}

		token := strings.TrimPrefix(auth, "Bearer ")
		// 使用恒定时间比较防止时序攻击 / Constant-time comparison to prevent timing attacks
		for _, item := range cfg.APIKeys {
			if subtle.ConstantTimeCompare([]byte(token), []byte(item.Key)) == 1 {
				c.Set("team_id", item.TeamID)
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(401, gin.H{"error": "invalid api key"})
	}
}

// IdentityMiddleware 身份注入中间件 / Identity injection middleware
func IdentityMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		teamIDVal, exists := c.Get("team_id")
		if !exists {
			c.AbortWithStatusJSON(500, gin.H{"error": "team_id not set by auth middleware"})
			return
		}
		teamID, ok := teamIDVal.(string)
		if !ok {
			c.AbortWithStatusJSON(500, gin.H{"error": "team_id has invalid type"})
			return
		}

		ownerID := c.GetHeader("X-User-ID")
		// 禁止客户端冒充系统身份 / Prevent clients from impersonating the system identity
		if ownerID == "" || ownerID == "__system__" {
			ownerID = "anonymous"
		}

		identity := &model.Identity{
			TeamID:  teamID,
			OwnerID: ownerID,
		}
		SetIdentity(c, identity)
		c.Next()
	}
}

// SetIdentity 将身份信息写入请求上下文 / Set identity into request context
func SetIdentity(c *gin.Context, id *model.Identity) {
	c.Set(identityKey, id)
}

// GetIdentity 从请求上下文获取身份 / Get identity from request context
func GetIdentity(c *gin.Context) *model.Identity {
	val, exists := c.Get(identityKey)
	if !exists {
		return nil
	}
	id, ok := val.(*model.Identity)
	if !ok {
		return nil
	}
	return id
}

// LoggerMiddleware 请求日志中间件 / Request logging middleware
func LoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		logger.Info("request completed",
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.Int("status", status),
			zap.Duration("latency", latency),
			zap.String("client_ip", c.ClientIP()),
		)
	}
}

// CORSMiddleware 跨域中间件，支持可配置的允许来源列表 / CORS middleware with configurable allowed origins
func CORSMiddleware(allowedOrigins []string) gin.HandlerFunc {
	// 构建来源集合 / Build origin set for O(1) lookup
	originSet := make(map[string]bool, len(allowedOrigins))
	wildcard := false
	for _, o := range allowedOrigins {
		if o == "*" {
			wildcard = true
		}
		originSet[o] = true
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		allowed := wildcard || originSet[origin]
		if allowed && origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		} else if wildcard {
			c.Header("Access-Control-Allow-Origin", "*")
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-User-ID")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}

// MaxBodySizeMiddleware 请求体大小限制中间件 / Request body size limit middleware
func MaxBodySizeMiddleware(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

// ipLimiterEntry 带最后访问时间的限流器 / Rate limiter with last-access tracking
type ipLimiterEntry struct {
	limiter    *rate.Limiter
	lastAccess time.Time
}

// RateLimitMiddleware 基于客户端 IP 的速率限制中间件 / Per-IP rate limiting middleware
// rps: 每秒允许请求数, burst: 突发上限
func RateLimitMiddleware(rps float64, burst int) gin.HandlerFunc {
	var mu sync.Mutex
	limiters := make(map[string]*ipLimiterEntry)
	ttl := 10 * time.Minute

	getLimiter := func(key string) *rate.Limiter {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		if entry, ok := limiters[key]; ok {
			entry.lastAccess = now
			return entry.limiter
		}
		lim := rate.NewLimiter(rate.Limit(rps), burst)
		limiters[key] = &ipLimiterEntry{limiter: lim, lastAccess: now}

		// 惰性清理：当 map 超过 1000 条时清理过期条目 / Lazy cleanup when map exceeds 1000 entries
		if len(limiters) > 1000 {
			for k, v := range limiters {
				if now.Sub(v.lastAccess) > ttl {
					delete(limiters, k)
				}
			}
		}
		return lim
	}

	return func(c *gin.Context) {
		key := c.ClientIP()
		if !getLimiter(key).Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}

// SecurityHeadersMiddleware 安全响应头中间件 / Security response headers middleware
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Next()
	}
}
