package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"iclude/internal/api"
	"iclude/internal/config"

	"github.com/gin-gonic/gin"
)

func TestAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	authCfg := config.AuthConfig{
		Enabled: true,
		APIKeys: []config.APIKeyItem{
			{Key: "sk-test-key", TeamID: "team-abc", Name: "test"},
		},
	}

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{"valid key", "Bearer sk-test-key", http.StatusOK},
		{"invalid key", "Bearer sk-wrong", http.StatusUnauthorized},
		{"missing header", "", http.StatusUnauthorized},
		{"malformed header", "sk-test-key", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.Use(api.AuthMiddleware(authCfg))
			r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

			req := httptest.NewRequest("GET", "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestAuthMiddleware_Disabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(api.AuthMiddleware(config.AuthConfig{Enabled: false}))
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("disabled auth should pass, got status %d", w.Code)
	}
}

func TestIdentityMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name      string
		userIDHdr string
		wantOwner string
	}{
		{"with X-User-ID", "alice", "alice"},
		{"without X-User-ID", "", "anonymous"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			// Simulate AuthMiddleware setting team_id
			r.Use(func(c *gin.Context) { c.Set("team_id", "team-test"); c.Next() })
			r.Use(api.IdentityMiddleware())
			r.GET("/test", func(c *gin.Context) {
				id := api.GetIdentity(c)
				if id == nil {
					t.Fatal("identity should not be nil")
				}
				if id.OwnerID != tt.wantOwner {
					t.Errorf("owner = %q, want %q", id.OwnerID, tt.wantOwner)
				}
				if id.TeamID != "team-test" {
					t.Errorf("team = %q, want 'team-test'", id.TeamID)
				}
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest("GET", "/test", nil)
			if tt.userIDHdr != "" {
				req.Header.Set("X-User-ID", tt.userIDHdr)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
		})
	}
}
