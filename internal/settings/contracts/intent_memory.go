package contracts

import "context"

type IntentDestination struct {
	Endpoint    string
	Fingerprint string
}

type IntentMemoryDisclosure interface {
	IntentMemoryRouteCurrent(context.Context, IntentDestination) bool
	MayDiscloseIntentMemory(context.Context, int64, int64, IntentDestination, string) bool
}
