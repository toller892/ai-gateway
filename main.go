package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"ai-gateway/internal/config"
	"ai-gateway/internal/proxy"
)

var (
	configPath = flag.String("config", "config.yaml", "path to config.yaml")
	port       = flag.Int("port", 8080, "listen port")
)

func main() {
	flag.Parse()

	// Load config
	configFile := *configPath
	if envPath := os.Getenv("CONFIG_PATH"); envPath != "" {
		configFile = envPath
	}

	if err := config.Load(configFile); err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	if config.GlobalConfig.Port > 0 {
		*port = config.GlobalConfig.Port
	}

	models := config.ListModels()
	log.Printf("ai-gateway started on :%d", *port)
	log.Printf("registered models: %d — %v", len(models), models)

	// Start server
	h := proxy.NewHandler()
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.ServeHTTP)

	ln, err := net.Listen("tcp4", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("listen tcp4: %v", err)
	}
	log.Printf("ai-gateway listening on %s", ln.Addr())

	// Graceful shutdown
	go func() {
		if err := http.Serve(ln, mux); err != nil {
			log.Printf("server exit: %v", err)
		}
	}()

	// Wait for interrupt
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch

	log.Println("shutting down...")
}
