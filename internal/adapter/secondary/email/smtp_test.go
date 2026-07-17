package email

import (
	"strings"
	"testing"

	"github.com/johnnycube/cairn-core/internal/config"
	"github.com/johnnycube/cairn-core/internal/port"
)

func newTestSender(t *testing.T) *SMTPSender {
	t.Helper()
	s, err := NewSMTP(config.EmailConfig{
		Adapter:     "smtp",
		FromAddress: "no-reply@cairn.local",
		FromName:    "Cairn",
		SMTPHost:    "localhost",
		SMTPPort:    1025,
	})
	if err != nil {
		t.Fatalf("NewSMTP: %v", err)
	}
	return s
}

func TestBuildMessage_TextOnly(t *testing.T) {
	s := newTestSender(t)
	msg := string(s.buildMessage(port.EmailMessage{
		To:       "alice@example.com",
		Subject:  "Hello",
		TextBody: "plain body",
	}))
	for _, want := range []string{
		"From: Cairn <no-reply@cairn.local>\r\n",
		"To: alice@example.com\r\n",
		"Subject: Hello\r\n",
		"Content-Type: text/plain; charset=UTF-8",
		"plain body",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q\n---\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "multipart") {
		t.Error("text-only message should not be multipart")
	}
}

func TestBuildMessage_HTMLMultipart(t *testing.T) {
	s := newTestSender(t)
	msg := string(s.buildMessage(port.EmailMessage{
		To:       "bob@example.com",
		Subject:  "Hi",
		TextBody: "text part",
		HTMLBody: "<p>html part</p>",
	}))
	for _, want := range []string{
		"Content-Type: multipart/alternative; boundary=",
		"Content-Type: text/plain; charset=UTF-8",
		"text part",
		"Content-Type: text/html; charset=UTF-8",
		"<p>html part</p>",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q\n---\n%s", want, msg)
		}
	}
}

func TestNewSMTP_RequiresHostAndFrom(t *testing.T) {
	if _, err := NewSMTP(config.EmailConfig{FromAddress: "x@y.z"}); err == nil {
		t.Error("expected error when SMTP host missing")
	}
	if _, err := NewSMTP(config.EmailConfig{SMTPHost: "h"}); err == nil {
		t.Error("expected error when from-address missing")
	}
}
