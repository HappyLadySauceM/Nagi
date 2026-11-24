package domain

type BackgroundEvidence struct {
	Version       int
	Destination   string
	RoleLayers    []BackgroundRoleLayer
	RoleHash      string
	Memories      []BackgroundMemoryRef
	MemoryOutcome string `json:",omitempty"`
}

type BackgroundRoleLayer struct {
	VersionID int64
	Version   int
	Scope     string
}

type BackgroundMemoryRef struct {
	ID              string
	SourceMessageID int64
	Digest          string
}

type SemanticUsage struct {
	InputTokens     int64
	OutputTokens    int64
	Responses       int64
	InputResponses  int64 `json:",omitempty"`
	OutputResponses int64 `json:",omitempty"`
}

type SemanticBackground struct {
	Role     *PromptIntentContext
	Memories []string
}

func (u SemanticUsage) InputValue() *int64 {
	if u.InputResponses == 0 {
		return nil
	}
	return &u.InputTokens
}

func (u SemanticUsage) OutputValue() *int64 {
	if u.OutputResponses == 0 {
		return nil
	}
	return &u.OutputTokens
}

type SemanticAttemptBudget struct {
	RequestReserved       bool
	RequestHeld           bool
	RemainingRequests     int
	ReservedAmountMicros  *int64 `json:",omitempty"`
	HeldAmountMicros      *int64 `json:",omitempty"`
	RemainingAmountMicros *int64 `json:",omitempty"`
	ReservationCurrency   string `json:",omitempty"`
	PriceVersion          string `json:",omitempty"`
}

type SemanticAttempt struct {
	Budget          *SemanticAttemptBudget `json:",omitempty"`
	Outcome         string
	Memory          bool
	InputTokens     *int64 `json:",omitempty"`
	OutputTokens    *int64 `json:",omitempty"`
	CostBasis       string
	Currency        string `json:",omitempty"`
	PartialWrite    bool   `json:",omitempty"`
	ReservationHeld bool
}
