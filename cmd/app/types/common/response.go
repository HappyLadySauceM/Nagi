// Package common defines the unified {code,message,data} response envelope.
// Package common 定义管理 API 的统一 {code,message,data} 响应信封。
package common

import (
	"encoding/json"
	"net/http"
)

// Envelope is the unified response envelope; Code mirrors the HTTP status.
// Envelope 是统一响应信封；Code 与 HTTP 状态码保持一致。
type Envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Success wraps data into a 200 envelope.
// Success 将数据包装为 200 信封。
func Success(data any) Envelope {
	return Envelope{Code: http.StatusOK, Message: "success", Data: data}
}

// Fail builds an error envelope; message must be safe to expose.
// Fail 构造错误信封；message 必须是可以对外暴露的安全信息。
func Fail(status int, message string) Envelope {
	return Envelope{Code: status, Message: message}
}

// WriteJSON writes the envelope with the given status.
// WriteJSON 以指定状态码写出信封。
func WriteJSON(w http.ResponseWriter, status int, payload Envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// WriteSuccess writes a 200 envelope with data.
// WriteSuccess 以 200 写出携带数据的信封。
func WriteSuccess(w http.ResponseWriter, data any) {
	WriteJSON(w, http.StatusOK, Success(data))
}

// WriteFailure writes an error envelope; message must be safe to expose.
// WriteFailure 写出错误信封；message 必须是可以对外暴露的安全信息。
func WriteFailure(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, Fail(status, message))
}
