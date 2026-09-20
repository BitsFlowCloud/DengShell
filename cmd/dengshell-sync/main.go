// Standalone ciphertext-only server, for NAS/VPS or background service use.
package main

import (
	"cloudshell/internal/syncserver"
	"cloudshell/internal/syncvault"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ip := flag.String("ip", "127.0.0.1", "IP address clients connect to (certificate SAN)")
	listen := flag.String("listen", "", "bind address, defaults to --ip; use 0.0.0.0 in Docker")
	port := flag.Int("port", 18443, "HTTPS port")
	dir := flag.String("data", "./sync-data", "private service data directory")
	flag.Parse()
	s, e := syncserver.Open(*dir, *ip, *port)
	if e != nil {
		log.Fatal(e)
	}
	defer s.Close()
	if *listen == "" {
		*listen = *ip
	}
	if e = s.Start(*listen, *port); e != nil {
		log.Fatal(e)
	}
	info := s.Info()
	if info.Bootstrap != "" {
		code, e := syncvault.Pack(info)
		if e != nil {
			log.Fatal(e)
		}
		fmt.Println("首次初始化连接资料（仅交给管理员，请勿公开）：\n" + code)
	}
	log.Printf("DengShell Sync listening at %s", info.URL)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
}
