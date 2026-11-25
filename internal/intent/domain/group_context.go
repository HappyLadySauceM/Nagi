package domain

import "time"

const (
	GroupWindow        = 120 * time.Second
	GroupRecentLimit   = 6
	GroupContextBudget = 4096
	GroupContextMeter  = "json-utf8-byte-upper-bound-v1"
)

type GroupContext struct {
	Version      string          `json:"version"`
	Current      GroupEvidence   `json:"current"`
	Messages     []GroupEvidence `json:"messages,omitempty"`
	Truncated    bool            `json:"truncated,omitempty"`
	HistoryUnits int             `json:"history_units"`
	HistoryLimit int             `json:"history_limit"`
	Meter        string          `json:"meter"`
}

type GroupEvidence struct {
	MessageID       int64    `json:"message_id"`
	RowID           int64    `json:"row_id,omitempty"`
	SenderID        int64    `json:"sender_id"`
	Bot             bool     `json:"bot"`
	Text            string   `json:"text"`
	MillisecondsAgo int64    `json:"milliseconds_ago"`
	Mentions        []string `json:"mentions,omitempty"`
	Replies         []string `json:"replies,omitempty"`
	Sources         []string `json:"sources"`
	Truncated       bool     `json:"truncated,omitempty"`
}
