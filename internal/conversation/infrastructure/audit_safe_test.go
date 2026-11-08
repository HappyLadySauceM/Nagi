package infrastructure

import (
	"testing"

	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

func TestAuditSafeSegmentsStripsInlineMedia(t *testing.T) {
	original := []obdomain.Segment{
		{Type: obdomain.SegImage, Data: map[string]string{"file": "base64://AAAA", "url": "https://example.invalid/image.png"}},
		{Type: obdomain.SegText, Data: map[string]string{"text": "hi"}},
	}
	safe := auditSafeSegments(original)
	if len(safe) != 2 {
		t.Fatalf("segments = %d", len(safe))
	}
	if safe[0].Data["file"] != "[inline]" {
		t.Fatalf("inline file = %q", safe[0].Data["file"])
	}
	if safe[0].Data["url"] != "https://example.invalid/image.png" {
		t.Fatalf("url = %q", safe[0].Data["url"])
	}
	if original[0].Data["file"] != "base64://AAAA" {
		t.Fatalf("original mutated: %q", original[0].Data["file"])
	}
}
