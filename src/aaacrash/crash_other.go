//go:build !windows

package crashlog

// Notify does nothing here: other platforms print to the terminal or the system log.
func Notify(title, text string) {}
