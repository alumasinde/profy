// Package notifications holds provider-independent delivery adapters (plan §18).
// Phase 2 uses them for OTP delivery; templates and the notification outbox arrive in Phase 6.
package notifications

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Message is a plain-text message to one destination.
type Message struct {
	To      string
	Subject string // email only
	Body    string
}

// Sender delivers a message on one channel.
type Sender interface {
	Send(ctx context.Context, m Message) error
	Channel() string
}

// FakeSender records messages in memory and logs them. Local/test only: config.Validate
// refuses it in production. It is the only component allowed to log message bodies.
type FakeSender struct {
	channel string
	log     *slog.Logger

	mu   sync.Mutex
	sent []Message
}

// NewFake builds a fake sender for a channel ("sms" or "email").
func NewFake(channel string, log *slog.Logger) *FakeSender {
	return &FakeSender{channel: channel, log: log}
}

// Channel implements Sender.
func (f *FakeSender) Channel() string { return f.channel }

// Send implements Sender.
func (f *FakeSender) Send(ctx context.Context, m Message) error {
	f.mu.Lock()
	f.sent = append(f.sent, m)
	f.mu.Unlock()
	f.log.InfoContext(ctx, "FAKE "+f.channel+" delivery (local only)", "to", m.To, "subject", m.Subject, "body", m.Body)
	return nil
}

// Last returns the most recent message sent to a destination (tests).
func (f *FakeSender) Last(to string) (Message, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.sent) - 1; i >= 0; i-- {
		if f.sent[i].To == to {
			return f.sent[i], true
		}
	}
	return Message{}, false
}

// Count returns how many messages were sent to a destination (tests).
func (f *FakeSender) Count(to string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.sent {
		if m.To == to {
			n++
		}
	}
	return n
}

// SMTPSender sends email over SMTP with STARTTLS (port 587) or implicit TLS (port 465).
type SMTPSender struct {
	Host, Username, Password, From string
	Port                           int
}

// Channel implements Sender.
func (s *SMTPSender) Channel() string { return "email" }

// Send implements Sender.
func (s *SMTPSender) Send(ctx context.Context, m Message) error {
	if strings.ContainsAny(m.To, "\r\n") || strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("smtp: header injection rejected")
	}
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	d := net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	if s.Port == 465 {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if s.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("smtp: server does not support STARTTLS")
		}
		if err := c.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if s.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(s.From); err != nil {
		return err
	}
	if err := c.Rcpt(m.To); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := "From: " + s.From + "\r\nTo: " + m.To + "\r\nSubject: " + m.Subject +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nDate: " +
		time.Now().UTC().Format(time.RFC1123Z) + "\r\n\r\n" + strings.ReplaceAll(m.Body, "\n", "\r\n")
	if _, err := io.WriteString(w, msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// AfricasTalkingSender sends SMS through the Africa's Talking messaging API.
type AfricasTalkingSender struct {
	BaseURL, Username, APIKey, SenderID string
	Client                              *http.Client
}

// Channel implements Sender.
func (a *AfricasTalkingSender) Channel() string { return "sms" }

// Send implements Sender. The response body is inspected for per-recipient status.
func (a *AfricasTalkingSender) Send(ctx context.Context, m Message) error {
	form := url.Values{"username": {a.Username}, "to": {m.To}, "message": {m.Body}}
	if a.SenderID != "" {
		form.Set("from", a.SenderID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.BaseURL, "/")+"/version1/messaging",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("apiKey", a.APIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sms send: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("sms send: HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		SMSMessageData struct {
			Recipients []struct {
				StatusCode int    `json:"statusCode"`
				Status     string `json:"status"`
			} `json:"Recipients"`
		} `json:"SMSMessageData"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("sms send: unexpected response")
	}
	if len(parsed.SMSMessageData.Recipients) == 0 {
		return errors.New("sms send: no recipients accepted")
	}
	for _, r := range parsed.SMSMessageData.Recipients {
		if r.StatusCode < 100 || r.StatusCode > 102 { // 100 Processed, 101 Sent, 102 Queued
			return fmt.Errorf("sms send: recipient status %d %s", r.StatusCode, r.Status)
		}
	}
	return nil
}
