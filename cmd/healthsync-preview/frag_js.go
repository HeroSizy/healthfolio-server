package main

import "syscall/js"

func fragment() string { return js.Global().Get("location").Get("hash").String() }
