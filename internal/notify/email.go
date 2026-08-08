package notify

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/smtp"
	"os"
	"strings"
	"time"
)

type EmailNotifier struct {
	host     string
	port     int
	username string
	password string
	from     string
	to       []string
}

func NewEmailNotifier(host string, port int, username, password, from string, to []string) *EmailNotifier {
	return &EmailNotifier{
		host:     host,
		port:     port,
		username: username,
		password: password,
		from:     from,
		to:       to,
	}
}

func (e *EmailNotifier) Send(subject, body string) error {
	addr := fmt.Sprintf("%s:%d", e.host, e.port)

	msg := e.buildMessage(subject, body)

	auth := smtp.PlainAuth("", e.username, e.password, e.host)

	return smtp.SendMail(addr, auth, e.from, e.to, []byte(msg))
}

func (e *EmailNotifier) Name() string {
	return "email"
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (e *EmailNotifier) buildMessage(subject, body string) string {
	hostname, _ := os.Hostname()
	now := time.Now()

	msgID := fmt.Sprintf("<%x.%s@%s>", now.UnixNano(), randomHex(4), hostname)

	headers := map[string]string{
		"From":                     e.from,
		"To":                       strings.Join(e.to, ", "),
		"Subject":                  subject,
		"Date":                     now.Format(time.RFC1123Z),
		"Message-ID":               msgID,
		"MIME-Version":             "1.0",
		"Content-Type":             "text/plain; charset=\"UTF-8\"",
		"Auto-Submitted":           "auto-generated",
		"Precedence":               "bulk",
		"X-Mailer":                 "dns-ha-agent",
		"X-Auto-Response-Suppress": "All",
	}

	var msg strings.Builder
	for k, v := range headers {
		msg.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	msg.WriteString("\r\n")
	msg.WriteString(body)
	msg.WriteString("\r\n")

	return msg.String()
}
