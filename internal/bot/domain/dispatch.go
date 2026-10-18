// Package domain provides pure dispatch policies.
// Package domain 提供事件分发的纯策略，仅依赖标准库。
package domain

// Descriptor snapshots one candidate handler and its match result.
// Descriptor 描述一个候选处理器与事件是否匹配的快照。
type Descriptor struct {
	Name  string
	Match bool
}

// Select returns matching handler names in registration order.
// Select 按注册顺序返回匹配的处理器名；无人匹配时返回空切片。
func Select(descriptors []Descriptor) []string {
	matched := make([]string, 0, len(descriptors))
	for _, d := range descriptors {
		if d.Match {
			matched = append(matched, d.Name)
		}
	}
	return matched
}
