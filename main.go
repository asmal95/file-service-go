package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"file-service-go/server"
	"file-service-go/service"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe http://127.0.0.1:$PORT/healthz and exit (for Docker HEALTHCHECK)")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthcheck())
	}

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	store, err := service.New(cfg.Dir, cfg.MaxStorageSize)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	srv := server.New(store, cfg.APIKeys, cfg.ServerURL, cfg.MaxFileSize)

	httpServer := &http.Server{
		Addr:              net.JoinHostPort("", cfg.Port),
		Handler:           srv.Handler(),
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      10 * time.Minute, // большие скачивания
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		total, _, count := store.Usage()
		log.Printf("listening :%s dir=%s files=%d used=%d/%d bytes keys=%d",
			cfg.Port, cfg.Dir, count, total, cfg.MaxStorageSize, len(cfg.APIKeys))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Print("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func runHealthcheck() int {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: status %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
