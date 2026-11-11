package logx

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	timeLayoutFile    = "2006-01-02 15:04:05.000"
	timeLayoutConsole = "15:04:05.000"
	componentWidth    = 8
)

var lineBreaks = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")

func oneLine(s string) string {
	return lineBreaks.Replace(s)
}

func quoteValue(v string) string {
	if v == "" {
		return `""`
	}
	if strings.ContainsAny(v, " \t\r\n\"=") || !utf8.ValidString(v) {
		return strconv.Quote(v)
	}
	return v
}

func token(key, value string) string {
	return key + "=" + quoteValue(value)
}

func padRight(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s + strings.Repeat(" ", n-len(s))
}

func formatLine(e Entry, file bool) string {
	layout := timeLayoutConsole
	if file {
		layout = timeLayoutFile
	}
	var b strings.Builder
	b.WriteString(e.Time.Format(layout) + " " + levelTag(e.level) + " " + padRight(e.Component, componentWidth) + " " + oneLine(e.Message))
	for _, f := range e.Fields {
		b.WriteString(" " + token(f.Key, f.Value))
	}
	if file && e.Stack != "" {
		b.WriteString(" " + token("stack", compactStack(e.Stack)))
	}
	b.WriteByte('\n')
	return b.String()
}

func LevelRank(name string) int { return levelRank(name) }
