package app

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"reef/internal/agents"
	"strings"
	"syscall"
	"time"
)

func Run() {
	configPath := flag.String("config", "config.json", "JSON configuration file")
	listen := flag.String("listen", "", "override the listening address")
	flag.Parse()
	c, e := loadConfig(*configPath)
	if e != nil {
		log.Fatal(e)
	}
	if *listen != "" {
		c.Listen = *listen
	}
	password := os.Getenv("QCU_PASSWORD")
	if file := os.Getenv("QCU_PASSWORD_FILE"); file != "" {
		b, e := os.ReadFile(file)
		if e != nil {
			log.Fatal("cannot read the password file")
		}
		password = strings.TrimRight(string(b), "\r\n")
	}
	if len(password) < 16 {
		log.Fatal("QCU_PASSWORD or QCU_PASSWORD_FILE is required (at least 16 characters)")
	}
	if e = os.MkdirAll(c.StateDir, 0700); e != nil {
		log.Fatal(e)
	}
	lock, e := os.OpenFile(filepath.Join(c.StateDir, "instance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		log.Fatal(e)
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		log.Fatal("Another instance is using this state directory")
	}
	m, e := newManager(c, DockerExecutor{})
	if e != nil {
		log.Fatal(e)
	}
	s := newServer(m, c, password)
	s.agents, e = agents.Open(c.StateDir)
	if e != nil {
		log.Fatal(e)
	}
	if e = s.restoreSessions(); e != nil {
		log.Fatal(e)
	}
	server := &http.Server{Addr: c.Listen, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	// Do not terminate workers on tab closure, or on graceful process shutdown.
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)
	stopped := make(chan struct{})
	go func() {
		<-done
		log.Print("Stopping new requests; waiting for active operations (up to 45 minutes). Do not force termination.")
		s.closeConsoles()
		_ = server.Shutdown(context.Background())
		m.wg.Wait()
		close(stopped)
	}()
	log.Printf("%s — http://%s", appName, c.Listen)
	if e = server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
	// Shutdown drains HTTP handlers before waiting for detached operations.
	<-stopped
}
