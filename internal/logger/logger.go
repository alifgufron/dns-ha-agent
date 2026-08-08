package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

type Logger struct {
	*slog.Logger
}

func New(level string, logFile string) *Logger {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "info":
		l = slog.LevelInfo
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}

	var out io.Writer = os.Stdout
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			slog.New(slog.NewTextHandler(os.Stderr, nil)).Error("cannot open log file, falling back to stdout", "file", logFile, "error", err)
		} else {
			out = f
		}
	}

	handler := &customHandler{
		out:   out,
		level: l,
	}

	return &Logger{slog.New(handler)}
}

func (l *Logger) Fatal(msg string, args ...any) {
	l.Error(msg, args...)
	os.Exit(1)
}

type customHandler struct {
	out    io.Writer
	level  slog.Leveler
	attrs  []slog.Attr
	groups []string
	prefix string
}

func (h *customHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *customHandler) Handle(_ context.Context, r slog.Record) error {
	ts := r.Time.Format(time.RFC3339)
	level := r.Level.String()

	var buf strings.Builder
	buf.WriteString(ts)
	buf.WriteString(" [")
	buf.WriteString(level)
	buf.WriteString("]")

	msg := r.Message
	// If msg starts with [TAG], extract it
	if strings.HasPrefix(msg, "[") {
		if idx := strings.Index(msg, "] "); idx > 0 {
			tag := msg[1:idx]
			msg = strings.TrimSpace(msg[idx+1:])
			buf.WriteString("[")
			buf.WriteString(tag)
			buf.WriteString("]")
		}
	}

	if msg != "" {
		buf.WriteString(" ")
		buf.WriteString(msg)
	}

	// Write attrs
	r.Attrs(func(a slog.Attr) bool {
		val := formatAttr(a)
		if val != "" {
			buf.WriteString(" ")
			buf.WriteString(a.Key)
			buf.WriteString("=")
			buf.WriteString(val)
		}
		return true
	})

	for _, a := range h.attrs {
		val := formatAttr(a)
		if val != "" {
			buf.WriteString(" ")
			buf.WriteString(a.Key)
			buf.WriteString("=")
			buf.WriteString(val)
		}
	}

	buf.WriteString("\n")

	_, err := h.out.Write([]byte(buf.String()))
	return err
}

func formatAttr(a slog.Attr) string {
	switch v := a.Value.Any().(type) {
	case string:
		return v
	case int:
		return fmt.Sprintf("%d", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case time.Duration:
		return v.String()
	case error:
		return v.Error()
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func (h *customHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &customHandler{
		out:    h.out,
		level:  h.level,
		attrs:  append(h.attrs, attrs...),
		groups: h.groups,
		prefix: h.prefix,
	}
}

func (h *customHandler) WithGroup(name string) slog.Handler {
	return &customHandler{
		out:    h.out,
		level:  h.level,
		attrs:  h.attrs,
		groups: append(h.groups, name),
		prefix: h.prefix,
	}
}
