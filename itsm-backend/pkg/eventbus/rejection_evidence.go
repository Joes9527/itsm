package eventbus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
)

// rejectionEvidence is an immutable observation, not a receipt authorizing ACK
// or a permanent verdict on a source which may later be authorized again.
// Never copy payload, UUID, metadata values, or arbitrary error strings here.
type rejectionEvidence struct {
	Status         string    `json:"status"`
	Reason         string    `json:"reason"`
	PayloadDigest  string    `json:"payloadDigest"`
	MessageDigest  string    `json:"messageDigest"`
	MetadataDigest string    `json:"metadataDigest"`
	FirstSeen      time.Time `json:"firstSeen"`
}

func eventDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func (eb *WatermillEventBus) recordRejection(ctx context.Context, consumer string, route streamRoute, msg *message.Message, reason string) {
	// Consumer and physical route come from the frozen subscription, never from
	// the untrusted envelope's tenant, deployment, or scope fields.
	if eb.rejectionClient == nil || !consumerIDPattern.MatchString(consumer) {
		eb.logger.Errorw("Persistent event rejection evidence unavailable", "reason", "recorder_unavailable")
		return
	}
	switch reason {
	case "envelope_invalid", "identity_mismatch", "source_rejected", "handler_rejected":
	default:
		reason = "handler_rejected"
	}
	record := rejectionEvidence{Status: "rejected", Reason: reason, PayloadDigest: eventDigest(msg.Payload), MessageDigest: eventDigest([]byte(msg.UUID)), MetadataDigest: eventDigest([]byte(msg.Metadata.Get("event_type"))), FirstSeen: time.Now().UTC()}
	// Fixed-width digests avoid delimiter ambiguity and retain no input secrets.
	fingerprint := eventDigest([]byte(record.PayloadDigest + record.MessageDigest + record.MetadataDigest))
	body, err := json.Marshal(record)
	if err != nil {
		eb.logger.Errorw("Persistent event rejection evidence unavailable", "reason", "encoding_failed")
		return
	}
	key := route.topic + ":rejections:" + consumer
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	created, err := eb.rejectionClient.HSetNX(writeCtx, key, fingerprint, string(body)).Result()
	if err != nil {
		eb.logger.Errorw("Persistent event rejection evidence unavailable", "reason", "storage_failed", "consumer", consumer)
		return
	}
	if created {
		eb.logger.Errorw("Persistent event rejected", "reason", reason, "consumer", consumer, "evidence_key", key, "fingerprint", fingerprint)
	}
}
