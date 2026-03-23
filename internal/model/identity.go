package model

// 可见性级别常量 / Visibility level constants
const (
	VisibilityPrivate = "private" // 仅创建者可见 / Owner only
	VisibilityTeam    = "team"    // 团队内可见 / Team members
	VisibilityPublic  = "public"  // 全局可见 / Everyone
)

// SystemOwnerID 系统内部操作使用的身份 / Identity for internal system operations
const SystemOwnerID = "__system__"

// Identity 请求身份信息 / Request identity context
type Identity struct {
	TeamID  string // 从 API Key 解析 / Resolved from API Key
	OwnerID string // 从 X-User-ID Header 提取 / From X-User-ID header
}

// IsSystem 是否为系统内部身份 / Whether this is a system identity
func (id *Identity) IsSystem() bool {
	return id.OwnerID == SystemOwnerID
}

// ValidVisibility 校验可见性值合法性 / Validate visibility value
func ValidVisibility(v string) bool {
	return v == VisibilityPrivate || v == VisibilityTeam || v == VisibilityPublic
}
