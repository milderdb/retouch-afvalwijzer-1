package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/stein155/retouch-afvalwijzer/plugin"
)

var version = "dev"

func main() {
	speaker := flagOr("--speaker-host", "127.0.0.1:8090")
	cfgDir := flagOr("--config-dir", "/mnt/nv/retouch/plugins/afvalwijzer")
	listen := flagOr("--listen", "127.0.0.1:9102")
	hostURL := flagOr("--host-url", "")

	logger := log.New(os.Stderr, "afvalwijzer: ", log.LstdFlags)
	logger.Printf("retouch-afvalwijzer %s starting", version)
	runtime.GOMAXPROCS(1)
	debug.SetGCPercent(20)
	debug.SetMemoryLimit(24 << 20)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeNamedPipe != 0 {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			logger.Printf("host closed stdin; shutting down")
			stop()
			time.Sleep(3 * time.Second)
			os.Exit(0)
		}()
	}

	p, err := plugin.New(ctx, cfgDir, speaker, hostURL, logger)
	if err != nil {
		logger.Fatalf("start: %v", err)
	}
	srv := &http.Server{Addr: listen, Handler: p.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sh)
	}()
	logger.Printf("listening on %s (speaker %s, config %s)", listen, speaker, cfgDir)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("listen: %v", err)
	}
}

func flagOr(name, def string) string {
	for i := 0; i < len(os.Args)-1; i++ {
		if os.Args[i] == name {
			return os.Args[i+1]
		}
	}
	return def
}
