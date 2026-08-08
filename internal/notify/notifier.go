package notify

type Notifier interface {
	Send(subject, body string) error
	Name() string
}
