package domain

// MemorySummary is the aggregate read model of one bot's memories.
// MemorySummary 是单个机器人记忆的聚合读模型。
type MemorySummary struct {
	Total        int64
	Active       int64
	Candidate    int64
	Superseded   int64
	ByVisibility map[Visibility]int64
}

// ExtractionSummary is the aggregate read model of one bot's extraction jobs.
// ExtractionSummary 是单个机器人抽取任务的聚合读模型。
type ExtractionSummary struct {
	PendingJobs    int64
	ProcessingJobs int64
	FailedJobs     int64
	LastErrorCode  string
}

// KnowledgeSummary combines memory and extraction aggregates for the
// knowledge page header. It contains counts only, never message or memory text.
// KnowledgeSummary 组合知识页头部所需的记忆与抽取聚合；只含计数，不含消息或记忆正文。
type KnowledgeSummary struct {
	Memories   MemorySummary
	Extraction ExtractionSummary
}
