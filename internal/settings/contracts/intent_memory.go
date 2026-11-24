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

type IntentDisclosureFence interface {
	IntentDisclosureRevision(context.Context, int64, int64, IntentDestination, bool) (int64, string)
	WithIntentDisclosure(context.Context, int64, int64, IntentDestination, bool, int64, func() error) (string, error)
}
