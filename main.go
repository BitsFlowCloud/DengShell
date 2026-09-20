package main

import (
	"cloudshell/internal/app"
	"embed"
	"flag"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"syscall"
)

//go:embed web
var assets embed.FS

func main() {
	if handleUpdateHelper() {
		return
	}
	detached := flag.Bool("detached-window", false, "internal independent window")
	browser := flag.Bool("browser", false, "run local browser mode instead of a desktop window")
	address := flag.String("addr", "127.0.0.1:0", "local API listen address")
	dev := flag.Bool("dev", false, "read frontend assets from the web directory")
	config := flag.String("config", "", "configuration directory (default: data beside executable; macOS app: Application Support/DengShell)")
	flag.Parse()
	var content fs.FS
	var contentErr error
	if *dev {
		content = os.DirFS("web")
	} else {
		content, contentErr = fs.Sub(assets, "web")
		if contentErr != nil {
			log.Fatal(contentErr)
		}
	}
	if *detached {
		if err := runDetachedProcess(content); err != nil {
			showStartupError(err.Error())
			log.Print(err)
		}
		return
	}

	if *config == "" {
		var err error
		*config, err = portableConfigDir()
		if err != nil {
			log.Fatal(err)
		}
	}
	if err := app.MigratePortableData(*config); err != nil {
		showStartupError(err.Error())
		log.Fatal(err)
	}
	application, err := app.New(*config)
	if err != nil {
		showStartupError(err.Error())
		log.Fatal(err)
	}
	defer application.Close()
	if err = application.Start(*address, content); err != nil {
		log.Fatal(err)
	}
	if !*browser && desktopAvailable() {
		log.Printf("DengShell: %s", application.URL())
		if err = runDesktop(application, content, *config); err != nil {
			log.Print(err)
		}
		return
	}
	log.Printf("DengShell: %s", application.BrowserURL())
	log.Print("本地浏览器模式：请打开上面的地址")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
}
