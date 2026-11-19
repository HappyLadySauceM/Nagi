package v1

import settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"

// UpdateRequest is the PUT body with optimistic concurrency on Version.
// UpdateRequest 是 PUT 请求体，Version 用于乐观并发控制。
type UpdateRequest struct {
	Settings settingsdomain.Settings `json:"Settings"`
	Version  int64                   `json:"Version"`
}
