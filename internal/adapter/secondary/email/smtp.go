// Package email implements port.EmailSender. Only the SMTP adapter is wired
// today (the EmailConfig.Adapter selector also names ses/resend/mailgun for
// future adapters). SMTP covers self-hosted relays and dev catchers like
// Mailpit, which is what the dev stack uses.
package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"

	"github.com/opencairn-org/cairn-core/internal/config"
	"github.com/opencairn-org/cairn-core/internal/port"
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
	fromName string // display name for the From header, may be empty
	fromAddr string // bare address for the envelope and the From header
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
	fromAddr, err := mail.ParseAddress(cfg.FromAddress)
	if err != nil || fromAddr.Name != "" {
		return nil, fmt.Errorf("email: FROM_ADDRESS %q is not a bare address", cfg.FromAddress)
	}
	if strings.ContainsAny(cfg.FromName, "\r\n") {
		return nil, errors.New("email: FROM_NAME must not contain line breaks")
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
		fromName: cfg.FromName,
		fromAddr: fromAddr.Address,
	}, nil
}

// Send delivers one message. The whole exchange is bounded by a 30s deadline.
func (s *SMTPSender) Send(ctx context.Context, msg port.EmailMessage) error {
	to, err := parseRecipient(msg.To)
	if err != nil {
		return err
	}
	m, err := s.compose(to, msg)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(s.host, fmt.Sprintf("%d", s.port))

	d := net.Dialer{Timeout: 30 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("email: dial %s: %w", addr, err)
	}
	c, err := smtp.NewClient(conn, s.host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("email: smtp client: %w", err)
	}
	defer func() { _ = c.Close() }()

	if err := s.smtpNegotiate(c); err != nil {
		return err
	}
	if err := c.Mail(s.fromAddr); err != nil {
		return fmt.Errorf("email: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return fmt.Errorf("email: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: DATA: %w", err)
	}
	if _, err := m.WriteTo(w); err != nil {
		return fmt.Errorf("email: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: close body: %w", err)
	}
	return c.Quit()
}

// smtpNegotiate runs the optional STARTTLS upgrade and authentication.
func (s *SMTPSender) smtpNegotiate(c *smtp.Client) error {
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
	return nil
}

// parseRecipient accepts a single bare address; display names, lists and line breaks are rejected.
func parseRecipient(raw string) (*mail.Address, error) {
	if raw == "" {
		return nil, errors.New("email: empty recipient")
	}
	to, err := mail.ParseAddress(raw)
	if err != nil {
		return nil, fmt.Errorf("email: invalid recipient: %w", err)
	}
	if to.Name != "" || to.Address != strings.TrimSpace(raw) {
		return nil, errors.New("email: recipient must be a bare address")
	}
	return to, nil
}

// compose builds the RFC 5322 message with go-mail, which validates the addresses and
// encodes the headers. multipart/alternative when an HTML body is supplied, otherwise text/plain.
func (s *SMTPSender) compose(to *mail.Address, msg port.EmailMessage) (*gomail.Msg, error) {
	if strings.ContainsAny(msg.Subject, "\r\n") {
		return nil, errors.New("email: subject must not contain line breaks")
	}
	m := gomail.NewMsg()
	if err := m.FromFormat(s.fromName, s.fromAddr); err != nil {
		return nil, fmt.Errorf("email: from: %w", err)
	}
	if err := m.To(to.Address); err != nil {
		return nil, fmt.Errorf("email: to: %w", err)
	}
	m.Subject(msg.Subject)
	m.SetBodyString(gomail.TypeTextPlain, msg.TextBody)
	if msg.HTMLBody != "" {
		m.AddAlternativeString(gomail.TypeTextHTML, msg.HTMLBody)
	}
	return m, nil
}

// Compile-time assertion.
var _ port.EmailSender = (*SMTPSender)(nil)
