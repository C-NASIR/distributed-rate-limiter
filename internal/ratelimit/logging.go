// Package ratelimit provides logging hooks.
package ratelimit

import (
	"encoding/json"
	"io"
	"log"
)

// Logger provides structured logging hooks.
type Logger interface {
	Info(msg string, fields map[string]any)
	Error(msg string, fields map[string]any)
}

// StdLogger logs to an io.Writer.
type StdLogger struct {
	l *log.Logger
}

// NewStdLogger constructs a StdLogger.
func NewStdLogger(w io.Writer) *StdLogger {
	return &StdLogger{l: log.New(w, "", log.LstdFlags)}
}

// Info logs an info message.
func (s *StdLogger) Info(msg string, fields map[string]any) {
	s.log("info", msg, fields)
}

// Error logs an error message.
func (s *StdLogger) Error(msg string, fields map[string]any) {
	s.log("error", msg, fields)
}

func (s *StdLogger) log(level string, msg string, fields map[string]any) {
	if s == nil || s.l == nil {
		return
	}
	payload := map[string]any{
		"level": level,
		"msg":   msg,
	}
	for key, value := range fields {
		payload[key] = value
	}
	data, err := json.Marshal(payload)
	if err != nil {
		s.l.Println(msg)
		return
	}
	s.l.Println(string(data))
}
