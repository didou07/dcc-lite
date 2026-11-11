package logx

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type handler struct {
	hub       *Hub
	component string
	prefix    string
	attrs     []Field
	stack     string
}

func (h *handler) target() *Hub {
	if h.hub != nil {
		return h.hub
	}
	return Default()
}

func (h *handler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.target().minLevel() }

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append([]Field(nil), h.attrs...)
	for _, a := range attrs {
		c.add(&c.attrs, &c.component, &c.stack, a)
	}
	return &c
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.prefix = h.prefix + name + "."
	return &c
}

func (h *handler) add(dst *[]Field, component, stack *string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	if a.Value.Kind() == slog.KindGroup {
		prefix := h.prefix
		if a.Key != "" {
			prefix += a.Key + "."
		}
		sub := *h
		sub.prefix = prefix
		for _, g := range a.Value.Group() {
			sub.add(dst, component, stack, g)
		}
		return
	}
	switch a.Key {
	case "component":
		*component = a.Value.String()
		return
	case "stack":
		*stack = cleanStack(a.Value.String())
		return
	}
	*dst = append(*dst, Field{Key: h.prefix + a.Key, Value: fieldValue(a)})
}

func fieldValue(a slog.Attr) string {
	switch {
	case isSensitiveKey(a.Key):
		return "***"
	case a.Value.Kind() == slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			return Redact(err.Error())
		}
		return Redact(fmt.Sprint(a.Value.Any()))
	case a.Value.Kind() == slog.KindDuration:
		return a.Value.Duration().Round(time.Millisecond).String()
	}
	return Redact(a.Value.String())
}

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	e := Entry{
		Time:      r.Time,
		level:     r.Level,
		Level:     LevelName(r.Level),
		Component: h.component,
		Message:   Redact(r.Message),
		Stack:     h.stack,
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.Fields = append(e.Fields, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		h.add(&e.Fields, &e.Component, &e.Stack, a)
		return true
	})
	if e.Component == "" {
		e.Component = "app"
	}
	h.target().emit(e)
	return nil
}
