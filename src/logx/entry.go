package logx

import (
	"log/slog"
	"time"
)

type Field struct {
	Key   string `json:"k"`
	Value string `json:"v"`
}

type Entry struct {
	ID        uint64    `json:"id"`
	Time      time.Time `json:"time"`
	Level     string    `json:"level"`
	Component string    `json:"component"`
	Message   string    `json:"message"`
	Fields    []Field   `json:"fields,omitempty"`
	Stack     string    `json:"stack,omitempty"`

	level slog.Level
}

func (e Entry) Field(key string) string {
	for _, f := range e.Fields {
		if f.Key == key {
			return f.Value
		}
	}
	return ""
}
