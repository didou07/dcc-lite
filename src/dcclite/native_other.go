//go:build !(windows && native) && !(linux && native) && !(darwin && native)

package main

const nativeBuild = false

func runNative(nativeDeps) error { return nil }
func nativeClose()               {}
func nativeNotice(string)        {}
