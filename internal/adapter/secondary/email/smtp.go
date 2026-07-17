// Package email implements port.EmailSender. Only the SMTP adapter is wired
// today (the EmailConfig.Adapter selector also names ses/resend/mailgun for
// future adapters). SMTP covers self-hosted relays and dev catchers like
// Mailpit, which is what the dev stack uses.
package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/johnnycube/cairn-core/internal/config"
	"github.com/johnnycube/cairn-core/internal/port"
)

// SMTPSender sends mail over SMTP, with optional STARTTLS + AUTH. For a plain
// dev catcher (Mailpit: no TLS, no auth) leave SMTPStartTLS=false and the
// username empty.
type SMTPSender struct {
	host     string
	port     int
	username string
	password string
	startTLS bool
	from     string // RFC5322 From header value, e.g. `Cairn <no-reply@…>`
	fromAddr string // bare address for the SMTP envelope
}

// NewSMTP builds an SMTP sender from config. Returns an error if the SMTP host
// or from-address is missing.
func NewSMTP(cfg config.EmailConfig) (*SMTPSender, error) {
	if cfg.SMTPHost == "" {
		return nil, fmt.Errorf("email: SMTP_HOST required for EMAIL_ADAPTER=smtp")
	}
	if cfg.FromAddress == "" {
		return nil, fmt.Errorf("email: FROM_ADDRESS required")
	}
	from := cfg.FromAddress
	if cfg.FromName != "" {
		from = fmt.Sprintf("%s <%s>", cfg.FromName, cfg.FromAddress)
	}
	p := cfg.SMTPPort
	if p == 0 {
		p = 587
	}
	return &SMTPSender{
		host:     cfg.SMTPHost,
		port:     p,
		username: cfg.SMTPUsername,
		password: cfg.SMTPPassword,
		startTLS: cfg.SMTPStartTLS,
		from:     from,
		fromAddr: cfg.FromAddress,
	}, nil
}

// Send delivers one message. The whole exchange is bounded by a 30s deadline.
func (s *SMTPSender) Send(ctx context.Context, msg port.EmailMessage) error {
	if msg.To == "" {
		return fmt.Errorf("email: empty recipient")
	}
	addr := net.JoinHostPort(s.host, fmt.Sprintf("%d", s.port))

	d := net.Dialer{Timeout: 30 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("email: dial %s: %w", addr, err)
	}
	c, err := smtp.NewClient(conn, s.host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("email: smtp client: %w", err)
	}
	defer c.Close()

	if s.startTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: s.host}); err != nil {
				return fmt.Errorf("email: starttls: %w", err)
			}
		}
	}
	if s.username != "" {
		auth := smtp.PlainAuth("", s.username, s.password, s.host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("email: auth: %w", err)
		}
	}
	if err := c.Mail(s.fromAddr); err != nil {
		return fmt.Errorf("email: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(msg.To); err != nil {
		return fmt.Errorf("email: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: DATA: %w", err)
	}
	if _, err := w.Write(s.buildMessage(msg)); err != nil {
		return fmt.Errorf("email: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: close body: %w", err)
	}
	return c.Quit()
}

// buildMessage renders the RFC5322 message. multipart/alternative when an HTML
// body is supplied, otherwise text/plain.
func (s *SMTPSender) buildMessage(msg port.EmailMessage) []byte {
	var b strings.Builder
	b.WriteString("From: " + s.from + "\r\n")
	b.WriteString("To: " + msg.To + "\r\n")
	b.WriteString("Subject: " + msg.Subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")

	if msg.HTMLBody == "" {
		b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		b.WriteString(msg.TextBody)
		return []byte(b.String())
	}

	const boundary = "cairn_alt_boundary_8f3a1c"
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	b.WriteString(msg.TextBody + "\r\n")
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
	b.WriteString(msg.HTMLBody + "\r\n")
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String())
}

// Compile-time assertion.
var _ port.EmailSender = (*SMTPSender)(nil)
