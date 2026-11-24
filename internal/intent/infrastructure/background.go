package infrastructure

import "github.com/HappyLadySauceM/Nagi/internal/intent/domain"

func addBackgroundState(state map[string]any, background domain.SemanticBackground) {
	if role := background.Role; role != nil {
		fields := make(map[string]any, 3)
		if role.Identity != "" {
			fields["identity"] = role.Identity
		}
		if len(role.Responsibilities) > 0 {
			fields["responsibilities"] = role.Responsibilities
		}
		if len(role.GroupConventions) > 0 {
			fields["group_conventions"] = role.GroupConventions
		}
		if len(fields) > 0 {
			state["role_reference"] = fields
		}
	}
	if len(background.Memories) > 0 {
		state["approved_group_facts"] = background.Memories
	}
}
