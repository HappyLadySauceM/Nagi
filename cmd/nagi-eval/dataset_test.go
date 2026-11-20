package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

func TestDialoguePreservesSignedReferenceWithoutRawIdentifiers(t *testing.T) {
	spec := selection{bot: 12999999, secret: []byte("fixture-key-is-not-a-production-secret")}
	message := domain.Message{MessageID: -51, SenderID: 22529999, Segments: []ob.Segment{
		ob.At(12999999), ob.At(82429999), ob.Reply(-37), ob.Text("Nagi 看看这个 https://example.org/private/22529999"),
	}}
	result := spec.dialogue(message)
	if !reflect.DeepEqual(result.Mentions, []string{"Nagi", spec.alias("person", "82429999")}) ||
		!reflect.DeepEqual(result.References, []string{spec.messageAlias(-37)}) ||
		result.ID != spec.messageAlias(-51) || strings.Contains(result.Text, "https://") ||
		strings.Contains(result.Text, "22529999") {
		t.Fatalf("unexpected dialogue %+v", result)
	}
}

func TestMissingMediaRequiresOneArchivedObjectPerAttachment(t *testing.T) {
	found := missingMedia([]string{"image", "image", "mface", "reply"}, []string{"image:uploaded", "image:expired", "mface:uploaded"})
	if !reflect.DeepEqual(found, []string{"media_image_unavailable"}) {
		t.Fatalf("missing media = %v", found)
	}
}
