package v1

import auditdomain "github.com/HappyLadySauceM/Nagi/internal/audit/domain"

// ArtifactResponse keeps the original artifact fields and adds an optional replay URL.
// ArtifactResponse 保留原有工件字段并附加可选的短时回放 URL。
type ArtifactResponse struct {
	auditdomain.AIArtifact
	URL string `json:"url,omitempty"`
}
