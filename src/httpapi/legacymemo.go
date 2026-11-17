package httpapi

import (
	"sync"
	"time"
)

const legacyMemoTTL = 6 * time.Hour

type legacyMemoEntry struct {
	choice string
	at     time.Time
}

var legacyMemo sync.Map // device base + "|" + function -> legacyMemoEntry

func legacyRecall(base, fn string) string {
	v, ok := legacyMemo.Load(base + "|" + fn)
	if !ok {
		return ""
	}
	e := v.(legacyMemoEntry)
	if time.Since(e.at) > legacyMemoTTL {
		legacyMemo.Delete(base + "|" + fn)
		return ""
	}
	return e.choice
}

func legacyRemember(base, fn, choice string) {
	key := base + "|" + fn
	if v, ok := legacyMemo.Load(key); ok && v.(legacyMemoEntry).choice == choice {
		legacyMemo.Store(key, legacyMemoEntry{choice, time.Now()})
		return
	}
	legacyMemo.Store(key, legacyMemoEntry{choice, time.Now()})
	devLog.Info("DreamOS address that works", "base", base, "function", fn, "address", choice)
}

func legacyForget(base, fn string) { legacyMemo.Delete(base + "|" + fn) }
