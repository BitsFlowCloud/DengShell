package main

import (
    "cloudshell/mobile"
    "fmt"
    "os"
    "os/signal"
)

func main() {
    dir, err := os.MkdirTemp("", "dengshell-android-smoke-")
    if err != nil { panic(err) }
    defer os.RemoveAll(dir)
    address, err := mobile.Start(dir)
    if err != nil { panic(err) }
    fmt.Println(address)
    ch := make(chan os.Signal, 1)
    signal.Notify(ch, os.Interrupt)
    <-ch
    mobile.Stop()
}
