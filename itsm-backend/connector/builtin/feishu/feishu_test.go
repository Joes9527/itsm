package feishu

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"itsm-backend/connector"
)

func TestVerifyEventSignature(t *testing.T) {
	c := NewClient("", "app", "sec", "", "ek1")
	ts, nonce, body := "1700000000", "abc123", []byte(`{"hello":"world"}`)
	h := sha256.New()
	h.Write([]byte("ek1"))
	h.Write([]byte(ts))
	h.Write([]byte(nonce))
	h.Write(body)
	sig := hex.EncodeToString(h.Sum(nil))
	if !c.VerifyEventSignature(ts, nonce, sig, body) {
		t.Fatal("expected signature to be valid")
	}
	if c.VerifyEventSignature(ts, nonce, "deadbeef", body) {
		t.Fatal("expected wrong signature to be invalid")
	}
}

func TestVerifyURLToken(t *testing.T) {
	c := NewClient("", "app", "sec", "tok-xyz", "")
	if !c.VerifyURLToken("tok-xyz") {
		t.Fatal("expected matching token to be valid")
	}
	if c.VerifyURLToken("nope") {
		t.Fatal("expected mismatched token to be invalid")
	}
}

func TestParseInbound_URLVerification(t *testing.T) {
	f := New()
	body := []byte(`{"type":"url_verification","challenge":"chal-1","token":"tok"}`)
	if f.client == nil {
		// 未 init 的情况下用临时 client 解析
		f.client = NewClient("", "app", "sec", "tok", "")
	}
	msg, err := f.ParseInbound(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg.Type != "url_verification" || msg.Content != "chal-1" {
		t.Fatalf("unexpected msg: %+v", msg)
	}
}

func TestParseInbound_MessageReceive(t *testing.T) {
	f := New()
	f.client = NewClient("", "app", "sec", "", "")
	body := []byte(`{
		"uuid":"u-1",
		"token":"t",
		"type":"event_callback",
		"ts":"1700000000",
		"header":{"app_id":"a","tenant_key":"tk","event_type":"im.message.receive_v1"},
		"event":{
			"sender":{"sender_id":{"open_id":"ou_123"}},
			"message":{"chat_id":"oc_1","chat_type":"p2p","message_id":"om_1","content":{"text":"hi"}}
		}
	}`)
	msg, err := f.ParseInbound(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg.UserID != "ou_123" || msg.Content != "hi" || msg.ChatID != "oc_1" {
		t.Fatalf("unexpected msg: %+v", msg)
	}
}

func TestBuildFeishuMessageBody(t *testing.T) {
	m := connector.Message{Channel: "ou_x", Type: "text", Content: "hello"}
	b := buildFeishuMessageBody(&m)
	if b["msg_type"] != "text" || b["receive_id"] != "ou_x" {
		t.Fatalf("unexpected body: %+v", b)
	}
}

func TestParseInboundRejectsAmbiguousJSON(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate type":    `{"type":"event_callback","type":"url_verification","challenge":"x"}`,
		"duplicate token":   `{"type":"url_verification","token":"wrong","token":"right"}`,
		"duplicate header":  `{"header":{},"header":{"event_type":"task.created"},"event":{}}`,
		"duplicate event":   `{"header":{"event_type":"task.created"},"event":{},"event":{}}`,
		"nested array":      `{"header":{"event_type":"task.created"},"event":{"items":[{"id":1,"id":2}]}}`,
		"escaped duplicate": `{"type":"url_verification","to\u006ben":"a","token":"b"}`,
		"multiple roots":    `{"type":"url_verification"}{}`,
		"null":              `null`, "array": `[]`, "malformed": `{"type":`,
		"oversized": `{"type":"url_verification","challenge":"` + strings.Repeat("a", 1<<20) + `"}`,
		"overdeep":  `{"type":"url_verification","extra":` + strings.Repeat("[", 65) + `0` + strings.Repeat("]", 65) + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			msg, err := New().ParseInbound([]byte(body))
			if err == nil || msg != nil {
				t.Fatalf("ambiguous payload accepted: err=%v", err)
			}
		})
	}
}
