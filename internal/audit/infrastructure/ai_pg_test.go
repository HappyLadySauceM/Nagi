package infrastructure

import (
	"context"
	"errors"
	"testing"
	"time"

	auditdomain "github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
	"github.com/google/uuid"
)

func TestAIRecorderFindsReusableAndExpiresMedia(t *testing.T) {
	pool := dbtest.Open(t)
	recorder, err := NewAIRecorder(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM ai_audit_artifacts`); err != nil {
		t.Fatal(err)
	}
	run, err := recorder.StartRun(ctx, auditdomain.AIRun{
		OperationID: "media-archive-" + uuid.NewString(), JobID: 1, BotID: 1,
		ConversationID: uuid.NewString(), TraceID: "trace",
	})
	if err != nil {
		t.Fatal(err)
	}
	contentHash := "hash-" + uuid.NewString()
	future := time.Now().UTC().Add(time.Hour)
	uploaded, err := recorder.RecordArtifact(ctx, auditdomain.AIArtifact{
		RunID: run.ID, Kind: "image.inbound", Status: "uploaded", SHA256: contentHash, ContentType: "image/png",
		SizeBytes: 3, ObjectKey: "nagi/artifacts/queued.png", Bucket: "nagi-audit", ExpiresAt: &future,
	})
	if err != nil {
		t.Fatal(err)
	}
	found, err := recorder.FindUploadedArtifactBySHA256(ctx, contentHash)
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != uploaded.ID || found.ObjectKey != uploaded.ObjectKey || found.Bucket != "nagi-audit" {
		t.Fatalf("found artifact = %+v", found)
	}
	if _, err := recorder.FindUploadedArtifactBySHA256(ctx, "missing-"+uuid.NewString()); !errors.Is(err, auditdomain.ErrNotFound) {
		t.Fatalf("missing lookup error = %v", err)
	}

	// An expired row must not lose its shared object while a newer retained row
	// still references the same bucket and key.
	expires := time.Now().UTC().Add(-time.Second)
	sharedKeeper, err := recorder.RecordArtifact(ctx, auditdomain.AIArtifact{
		RunID: run.ID, Kind: "image.inbound", Status: "uploaded", SHA256: "keep-" + uuid.NewString(),
		ContentType: "image/png", SizeBytes: 3, ObjectKey: "nagi/artifacts/shared.png", Bucket: "nagi-audit", ExpiresAt: &future,
	})
	if err != nil {
		t.Fatal(err)
	}
	sharedOld, err := recorder.RecordArtifact(ctx, auditdomain.AIArtifact{
		RunID: run.ID, Kind: "image.inbound", Status: "uploaded", SHA256: "old-" + uuid.NewString(),
		ContentType: "image/png", SizeBytes: 3, ObjectKey: "nagi/artifacts/shared.png", Bucket: "nagi-audit", ExpiresAt: &expires,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := recorder.ClaimExpiredArtifacts(ctx, 2, time.Minute); err != nil {
		t.Fatal(err)
	} else if len(claimed) != 0 {
		t.Fatalf("shared object claimed while retained: %+v", claimed)
	}

	expiredCandidate, err := recorder.RecordArtifact(ctx, auditdomain.AIArtifact{
		RunID: run.ID, Kind: "audio.outbound.synthesized", Status: "uploaded", SHA256: "expired-" + uuid.NewString(),
		ContentType: "audio/mpeg", SizeBytes: 3, ObjectKey: "nagi/artifacts/expired.mp3", Bucket: "nagi-audit", ExpiresAt: &expires,
	})
	if err != nil {
		t.Fatal(err)
	}
	expired, err := recorder.ClaimExpiredArtifacts(ctx, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].ID != expiredCandidate.ID || expired[0].LeaseToken == "" {
		t.Fatalf("claimed expiry = %+v", expired)
	}
	if err := recorder.MarkArtifactExpired(ctx, expiredCandidate.ID, expired[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	final, err := recorder.GetArtifact(ctx, expiredCandidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != "expired" || final.SHA256 != expiredCandidate.SHA256 {
		t.Fatalf("expired artifact = %+v", final)
	}
	if shared, err := recorder.GetArtifact(ctx, sharedOld.ID); err != nil || shared.Status != "uploaded" {
		t.Fatalf("shared expired artifact = %+v err=%v", shared, err)
	}
	if keeper, err := recorder.GetArtifact(ctx, sharedKeeper.ID); err != nil || keeper.Status != "uploaded" {
		t.Fatalf("shared retained artifact = %+v err=%v", keeper, err)
	}
}
