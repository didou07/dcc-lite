//go:build !windows

package main

import "os"

func osLang() string { return os.Getenv("LANG") }
