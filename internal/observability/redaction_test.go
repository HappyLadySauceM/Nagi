package observability

import (
	"strings"
	"testing"
)

func TestRedactRemovesNestedCredentialsAndSignedURLs(t *testing.T) {
	input := map[string]any{
		"Authorization": "Bearer very-secret-token",
		"nested": map[string]any{
			"cookie": "sid=private",
			"url":    "https://media.example/a.mp3?X-Amz-Signature=secret&safe=1",
		},
		"message": "use sk-abcdefghijklmnopqrstuvwxyz for this request",
	}
	got := Redact(input).(map[string]any)
	if got["Authorization"] != redactedSecret {
		t.Fatalf("authorization = %#v", got["Authorization"])
	}
	nested := got["nested"].(map[string]any)
	if nested["cookie"] != redactedSecret {
		t.Fatalf("cookie = %#v", nested["cookie"])
	}
	if want := "https://media.example/a.mp3?X-Amz-Signature=" + redactedSecret + "&safe=1"; nested["url"] != want {
		t.Fatalf("url = %q, want %q", nested["url"], want)
	}
	if got["message"] != "use "+redactedSecret+" for this request" {
		t.Fatalf("message = %q", got["message"])
	}
}

func TestRedactStringRemovesCredentialsFromSerializedJSON(t *testing.T) {
	got := RedactString(`{"Cookie":"sid=private","nested":{"Authorization":"Bearer secret"},"url":"https://media.example/a.mp3?X-Amz-Signature=secret&safe=1"}`)
	for _, secret := range []string{"sid=private", "Bearer secret", "X-Amz-Signature=secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("serialized JSON leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, redactedSecret) {
		t.Fatalf("serialized JSON was not redacted: %s", got)
	}
}
