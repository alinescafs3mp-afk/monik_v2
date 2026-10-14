package spark

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// This is a real unprivileged synthetic HTTP process, with no model/GPU/systemd
// effects. Its evidence is distinct from native systemd and real inference.
func TestNativeSyntheticEngineOutlivesMonitor(t *testing.T) {
	if os.Getenv("MONIK_SPARK_NATIVE_HELPER") == "1" {
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			os.Exit(2)
		}
		started := time.Now()
		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				http.Error(w, "method", 405)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if time.Since(started) < 800*time.Millisecond {
				w.WriteHeader(503)
				io.WriteString(w, `{"status":"loading","synthetic":true}`)
				return
			}
			io.WriteString(w, `{"status":"ok","synthetic":true}`)
		})
		server := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
		go server.Serve(listener)
		fmt.Println("SYNTHETIC http://" + listener.Addr().String() + "/health")
		var byte [1]byte
		_, _ = os.Stdin.Read(byte[:])
		_ = server.Close()
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestNativeSyntheticEngineOutlivesMonitor$")
	cmd.Env = append(os.Environ(), "MONIK_SPARK_NATIVE_HELPER=1")
	cmd.Dir = t.TempDir()
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	stdin, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	line, e := bufio.NewReader(stdout).ReadString('\n')
	if e != nil || !strings.HasPrefix(line, "SYNTHETIC ") {
		t.Fatal("synthetic engine startup missing")
	}
	url := strings.TrimSpace(strings.TrimPrefix(line, "SYNTHETIC "))
	profile := Profile{Engine: "llama.cpp", HealthURL: url, HealthScope: "deployment"}
	if readiness, _ := EngineReadiness(context.Background(), profile); readiness != "unready" {
		t.Fatalf("loading process misclassified %s", readiness)
	}
	worker, reports, mu := workerFixture(t, false)
	ctx, end := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		count := len(*reports)
		mu.Unlock()
		if count > 0 {
			break
		}
		if time.Now().After(deadline) {
			end()
			t.Fatal("monitor failed real TLS report")
		}
		time.Sleep(5 * time.Millisecond)
	}
	end()
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("monitor cleanup stalled")
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		readiness, reason := EngineReadiness(context.Background(), profile)
		if readiness == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("independent synthetic process not ready after monitor stop: %s %s", readiness, reason)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
