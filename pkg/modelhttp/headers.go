// Package modelhttp applies private model headers without forwarding them on redirects.
// Package modelhttp 应用私有模型请求头，且不通过重定向转发。
package modelhttp

import (
	"fmt"
	"maps"
	"net/http"
	"strings"
)

// Normalize validates header drafts without including their values in errors.
// Normalize 校验请求头草稿，错误不包含请求头值。
func Normalize(headers map[string]string) (map[string]string, error) {
	if len(headers) > 32 {
		return nil, fmt.Errorf("自定义请求头最多32项")
	}
	out := map[string]string{}
	total := 0
	for key, value := range headers {
		if key == "" || len(key) > 128 {
			return nil, fmt.Errorf("HTTP 请求头名称无效")
		}
		for _, r := range key {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
				return nil, fmt.Errorf("HTTP 请求头名称只能包含字母、数字及合法符号")
			}
		}
		key = http.CanonicalHeaderKey(key)
		switch key {
		case "Host", "Content-Length", "Connection", "Transfer-Encoding", "Upgrade", "Trailer", "Te", "Keep-Alive", "Proxy-Authorization", "Proxy-Authenticate":
			return nil, fmt.Errorf("该 HTTP 请求头由连接层管理，不能自定义")
		}
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("HTTP 请求头名称不能重复（不区分大小写）")
		}
		total += len(key) + len(value)
		if len(value) > 8192 || total > 32768 {
			return nil, fmt.Errorf("自定义请求头内容过长")
		}
		for _, r := range value {
			if r < 32 && r != '\t' || r == 127 {
				return nil, fmt.Errorf("HTTP 请求头值不能包含换行或控制字符")
			}
		}
		out[key] = value
	}
	return out, nil
}

type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	for k, v := range t.headers {
		clone.Header.Set(k, v)
	}
	return t.base.RoundTrip(clone)
}

// Client copies a client and freezes its private headers; redirects remain responses.
// Client 复制客户端并固定私有请求头，重定向作为响应返回。
func Client(base *http.Client, headers map[string]string) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = headerTransport{base: transport, headers: maps.Clone(headers)}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}
