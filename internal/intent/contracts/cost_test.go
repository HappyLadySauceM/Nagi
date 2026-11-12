package contracts

import (
	"strings"
	"testing"
)

func TestSemanticCostEstimateDoesNotPromiseOneRequest(t *testing.T) {
	if strings.Contains(SemanticCostEstimate, "one semantic request") ||
		!strings.Contains(SemanticCostEstimate, "多次模型请求") {
		t.Fatalf("cost estimate must not promise a single provider request: %q", SemanticCostEstimate)
	}
}
