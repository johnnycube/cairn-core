package email

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/mail"
	"strconv"
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

func render(t *testing.T, s *SMTPSender, msg port.EmailMessage) string {
	t.Helper()
	to, err := parseRecipient(msg.To)
	if err != nil {
		t.Fatalf("parseRecipient: %v", err)
	}
	m, err := s.compose(to, msg)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	var buf bytes.Buffer
	if _, err := m.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	return buf.String()
}

func TestCompose_TextOnly(t *testing.T) {
	msg := render(t, newTestSender(t), port.EmailMessage{
		To:       "alice@example.com",
		Subject:  "Hello",
		TextBody: "plain body",
	})
	for _, want := range []string{
		`From: "Cairn" <no-reply@cairn.local>`,
		"To: <alice@example.com>",
		"Subject: Hello",
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

func TestCompose_HTMLMultipart(t *testing.T) {
	msg := render(t, newTestSender(t), port.EmailMessage{
		To:       "bob@example.com",
		Subject:  "Hi",
		TextBody: "text part",
		HTMLBody: "<p>html part</p>",
	})
	for _, want := range []string{
		"Content-Type: multipart/alternative;",
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

func TestCompose_EncodesNonASCIISubject(t *testing.T) {
	msg := render(t, newTestSender(t), port.EmailMessage{To: "a@example.com", Subject: "Grüße", TextBody: "x"})
	if !strings.Contains(msg, "Subject: =?UTF-8?") {
		t.Errorf("subject not RFC 2047-encoded\n---\n%s", msg)
	}
}

func TestCompose_RejectsSubjectLineBreaks(t *testing.T) {
	s := newTestSender(t)
	to := &mail.Address{Address: "a@example.com"}
	for _, subj := range []string{"hi\r\nBcc: evil@example.com", "hi\nX: y", "hi\rX: y"} {
		if _, err := s.compose(to, port.EmailMessage{To: to.Address, Subject: subj}); err == nil {
			t.Errorf("subject %q accepted", subj)
		}
	}
}

func TestParseRecipient(t *testing.T) {
	for _, ok := range []string{"alice@example.com", "a.b+tag@sub.example.org"} {
		if _, err := parseRecipient(ok); err != nil {
			t.Errorf("parseRecipient(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{
		"",
		"not-an-address",
		"alice@example.com\r\nBcc: evil@example.com",
		"alice@example.com\nBcc: evil@example.com",
		"Alice <alice@example.com>",
		"a@example.com, b@example.com",
	} {
		if _, err := parseRecipient(bad); err == nil {
			t.Errorf("parseRecipient(%q) accepted", bad)
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

func TestNewSMTP_RejectsBadFrom(t *testing.T) {
	for _, cfg := range []config.EmailConfig{
		{SMTPHost: "h", FromAddress: "Cairn <x@y.z>"},
		{SMTPHost: "h", FromAddress: "nope"},
		{SMTPHost: "h", FromAddress: "x@y.z", FromName: "Cairn\r\nBcc: evil@example.com"},
	} {
		if _, err := NewSMTP(cfg); err == nil {
			t.Errorf("NewSMTP(%+v) accepted", cfg)
		}
	}
}

func TestNewSMTP_DefaultPort(t *testing.T) {
	s, err := NewSMTP(config.EmailConfig{SMTPHost: "h", FromAddress: "x@y.z"})
	if err != nil {
		t.Fatal(err)
	}
	if s.port != 587 {
		t.Errorf("port = %d, want 587", s.port)
	}
}

func TestSend_RejectsBadInputBeforeDialing(t *testing.T) {
	s := newTestSender(t)
	s.host = "invalid.invalid" // a dial would fail with a different error
	for _, msg := range []port.EmailMessage{
		{To: "", Subject: "x"},
		{To: "a@example.com\r\nBcc: evil@example.com", Subject: "x"},
		{To: "a@example.com", Subject: "x\r\nBcc: evil@example.com"},
	} {
		err := s.Send(context.Background(), msg)
		if err == nil || strings.Contains(err.Error(), "dial") {
			t.Errorf("Send(%+v) = %v, want a validation error", msg, err)
		}
	}
}

// fakeSMTP accepts one plain SMTP session and returns the envelope and DATA it received.
func fakeSMTP(t *testing.T) (port int, got <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	out := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			out <- ""
			return
		}
		defer func() { _ = conn.Close() }()
		r := bufio.NewReader(conn)
		reply := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		var log strings.Builder
		reply("220 fake ESMTP")
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				out <- log.String()
				return
			}
			log.WriteString(line)
			switch {
			case inData && line == ".\r\n":
				inData = false
				reply("250 queued")
			case inData:
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				reply("250 fake")
			case strings.HasPrefix(line, "DATA"):
				inData = true
				reply("354 go ahead")
			case strings.HasPrefix(line, "QUIT"):
				reply("221 bye")
				out <- log.String()
				return
			default:
				reply("250 ok")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, out
}

func TestSend_DeliversOverSMTP(t *testing.T) {
	p, got := fakeSMTP(t)
	s, err := NewSMTP(config.EmailConfig{SMTPHost: "127.0.0.1", SMTPPort: p, FromAddress: "no-reply@cairn.local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), port.EmailMessage{To: "alice@example.com", Subject: "Hi", TextBody: "body"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := <-got
	for _, want := range []string{"MAIL FROM:<no-reply@cairn.local>", "RCPT TO:<alice@example.com>", "Subject: Hi", "body"} {
		if !strings.Contains(session, want) {
			t.Errorf("session missing %q\n---\n%s", want, session)
		}
	}
}

func TestSend_DialError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	s, err := NewSMTP(config.EmailConfig{SMTPHost: "127.0.0.1", SMTPPort: p, FromAddress: "x@y.z"})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Send(context.Background(), port.EmailMessage{To: "a@example.com", Subject: "x"})
	if err == nil || !strings.Contains(err.Error(), "dial 127.0.0.1:"+strconv.Itoa(p)) {
		t.Errorf("Send = %v, want dial error", err)
	}
}
