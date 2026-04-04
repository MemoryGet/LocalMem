package identity

import (
	"crypto/sha256"
	"fmt"
)

// ResolveProjectID generates a stable project_id from directory path / 根据目录路径生成稳定 project_id
func ResolveProjectID(projectDir string) string {
	if projectDir == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(projectDir))
	return fmt.Sprintf("p_%x", hash[:6])
}
