package model

import "time"

// ScopePolicy scope 写入权限策略 / Scope write permission policy
type ScopePolicy struct {
	ID             string    `json:"id"`
	Scope          string    `json:"scope"`           // e.g. "project/p_a1b2c3"
	DisplayName    string    `json:"display_name"`
	TeamID         string    `json:"team_id"`
	AllowedWriters []string  `json:"allowed_writers"` // owner_id 列表 / List of owner_ids
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// CanWrite 检查 owner_id 是否有写入权限 / Check if owner_id has write permission
// 无策略或空白名单 = 不限制 / No policy or empty writers = unrestricted
func (p *ScopePolicy) CanWrite(ownerID string) bool {
	if p == nil || len(p.AllowedWriters) == 0 {
		return true
	}
	for _, w := range p.AllowedWriters {
		if w == ownerID {
			return true
		}
	}
	return false
}
