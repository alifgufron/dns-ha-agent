package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// webhookNotifier sends subject+body to a generic JSON webhook (Slack).
type webhookNotifier struct {
	name string
	url  string
}

func NewSlackNotifier(webhookURL string) Notifier {
	return &webhookNotifier{name: "slack", url: webhookURL}
}

func (n *webhookNotifier) Send(subject, body string) error {
	payload, err := json.Marshal(map[string]string{
		"text": subject + "\n\n" + body,
	})
	if err != nil {
		return fmt.Errorf("slack: marshal payload: %w", err)
	}
	return postJSON(n.url, payload)
}

func (n *webhookNotifier) Name() string {
	return n.name
}

// telegramNotifier sends via Telegram Bot API.
type telegramNotifier struct {
	name string
	url  string
	chat string
}

func NewTelegramNotifier(botToken, chatID string) Notifier {
	return &telegramNotifier{
		name: "telegram",
		url:  fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken),
		chat: chatID,
	}
}

func (n *telegramNotifier) Send(subject, body string) error {
	// Telegram text limit is 4096 chars — truncate to stay within.
	text := subject + "\n\n" + body
	if len(text) > 4000 {
		text = text[:4000] + "\n...[truncated]"
	}
	payload, err := json.Marshal(map[string]string{
		"chat_id": n.chat,
		"text":    text,
	})
	if err != nil {
		return fmt.Errorf("telegram: marshal payload: %w", err)
	}
	return postJSON(n.url, payload)
}

func (n *telegramNotifier) Name() string {
	return n.name
}

func postJSON(url string, payload []byte) error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}
