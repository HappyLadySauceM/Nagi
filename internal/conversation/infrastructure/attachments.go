package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	em "github.com/HappyLadySauceM/Nagi/pkg/db/ent/message"
	"strconv"
	"time"

	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
)

func registerInboundAttachments(ctx context.Context, client *ent.Client, messageID int64, expires time.Time, segments []ob.Segment) error {
	var tasks []*ent.InboundAttachmentCreate
	for index, segment := range segments {
		switch segment.Type {
		case ob.SegImage, ob.SegRecord, ob.SegVideo, "file", ob.SegFace, "mface", "bface":
		default:
			continue
		}
		task := client.InboundAttachment.Create().SetMessageID(messageID).
			SetSegmentIndex(index).SetKind(string(segment.Type)).SetExpiresAt(expires)
		source := make(map[string]string)
		for _, key := range []string{"url", "file", "file_id", "name", "file_name", "file_size", "busid", "file_hash"} {
			if value := segment.Data[key]; value != "" {
				source[key] = value
			}
		}
		if !media.HasDownloadSource(segment) {
			task.SetStatus("unavailable").SetFailureCode("no_download_source")
		} else {
			payload, err := json.Marshal(source)
			if err != nil {
				return err
			}
			task.SetSource(payload)
		}
		tasks = append(tasks, task)
	}
	if len(tasks) == 0 {
		return nil
	}
	return client.InboundAttachment.CreateBulk(tasks...).OnConflict(entsql.ConflictColumns("message_id", "segment_index"), entsql.DoNothing()).Exec(ctx)
}

func isFileNotice(event ob.Event) bool {
	kind := event.NoticeType()
	return kind == ob.NoticeGroupUpload || kind == "offline_file"
}

func fileNoticeMessage(event ob.Event) (ob.Event, bool) {
	if !isFileNotice(event) || event.UserID <= 0 {
		return ob.Event{}, false
	}
	file, ok := event.Extra["file"].(map[string]any)
	if !ok {
		return ob.Event{}, false
	}
	fields := map[string]string{}
	for key, value := range file {
		switch v := value.(type) {
		case string:
			fields[key] = v
		case float64:
			fields[key] = strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	id := fields["id"]
	if id == "" {
		id = fields["file_id"]
	}
	if id == "" && fields["url"] == "" {
		return ob.Event{}, false
	}
	identity := id
	if identity == "" {
		identity = fields["url"]
	}
	sum := sha256.Sum256([]byte(strconv.FormatInt(int64(event.UserID), 10) + "\x00" + identity))
	// Keep synthetic notice identifiers outside the signed 32-bit OneBot message range.
	event.MessageID = ob.MessageID((binary.BigEndian.Uint64(sum[:8]) & ((1 << 51) - 1)) | (1 << 52))
	event.Sender.ID = event.UserID
	event.Post = ob.PostMessage
	event.Type = ob.TypePrivate
	if event.GroupID > 0 {
		event.Type = ob.TypeGroup
	}
	fields["file_id"] = id
	fields["file_size"] = fields["size"]
	event.Segments = []ob.Segment{{Type: "file", Data: fields}}
	return event, true
}

// BackfillInboundAttachments queues only media still within the message's
// original retention window and does not create a decision job.
func (r *Repo) BackfillInboundAttachments(ctx context.Context, botID, messageID int64) error {
	if r.attachmentRetention <= 0 {
		return nil
	}
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	client := tx.Client()
	record, err := client.Message.Query().Where(em.ID(messageID), em.BotID(botID), em.DirectionEQ("incoming")).ForShare().Only(ctx)
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	message, err := messageModel(record)
	if err != nil {
		return err
	}
	expires := message.CreatedAt.Add(r.attachmentRetention)
	if !expires.After(time.Now()) {
		return nil
	}
	if err := registerInboundAttachments(ctx, client, messageID, expires, message.Segments); err != nil {
		return err
	}
	return tx.Commit()
}
