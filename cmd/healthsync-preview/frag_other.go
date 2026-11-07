//go:build !js

package main

import "os"

func fragment() string {
	if len(os.Args) > 1 {
		return os.Args[1]
	}
	return ""
}
