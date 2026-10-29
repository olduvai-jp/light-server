package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func dataRoot() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "light-server")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "share", "light-server")
}
func main() {
	if e := run(os.Args[1:]); e != nil {
		log.Print(e)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("使い方: light-server serve | project add/list/update/remove/purge (詳細: README.md)")
	}
	if args[0] == "serve" {
		f := flag.NewFlagSet("serve", flag.ContinueOnError)
		addr := f.String("addr", "127.0.0.1:8787", "待ち受け")
		root := f.String("data", dataRoot(), "保存ルート")
		if e := f.Parse(args[1:]); e != nil {
			return e
		}
		s, e := NewServer(*root)
		if e != nil {
			return e
		}
		defer s.Close()
		server := &http.Server{Addr: *addr, Handler: s, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		done := make(chan error, 1)
		go func() { log.Printf("listen=%s data=%s", *addr, s.root); done <- server.ListenAndServe() }()
		select {
		case e := <-done:
			if e != http.ErrServerClosed {
				return e
			}
		case <-ctx.Done():
			c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			return server.Shutdown(c)
		}
		return nil
	}
	if args[0] != "project" || len(args) < 2 {
		return fmt.Errorf("project add/list/update/remove/purge を指定してください")
	}
	action := args[1]
	id := ""
	rest := args[2:]
	if action != "list" {
		if len(rest) == 0 {
			return fmt.Errorf("プロジェクトIDが必要です")
		}
		id = rest[0]
		rest = rest[1:]
		if !validName.MatchString(id) {
			return fmt.Errorf("不正なプロジェクトID")
		}
	}
	f := flag.NewFlagSet("project", flag.ContinueOnError)
	base := f.String("server", envDefault("LIGHT_SERVER_URL", "http://127.0.0.1:8787"), "サーバーURL")
	dir := f.String("dir", "", "配信元")
	all := f.Bool("all", false, "解除済みも表示")
	if e := f.Parse(rest); e != nil {
		return e
	}
	if len(f.Args()) > 0 {
		return fmt.Errorf("不明な引数")
	}
	method, path := "GET", "/api/projects"
	var body io.Reader
	switch action {
	case "list":
		if *all {
			path += "?all=true"
		}
	case "add", "update":
		if *dir == "" {
			return fmt.Errorf("--dir が必要です")
		}
		abs, e := filepath.Abs(*dir)
		if e != nil {
			return e
		}
		b, _ := json.Marshal(map[string]string{"id": id, "dir": abs})
		body = bytes.NewReader(b)
		method = "POST"
		if action == "update" {
			method = "PUT"
			path += "/" + id
		}
	case "remove", "purge":
		method = "DELETE"
		path += "/" + id
		if action == "purge" {
			path += "?purge=true"
		}
	default:
		return fmt.Errorf("不明な操作: %s", action)
	}
	req, e := http.NewRequest(method, strings.TrimRight(*base, "/")+path, body)
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	res, e := client.Do(req)
	if e != nil {
		return fmt.Errorf("サーバーに接続できません: %w", e)
	}
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if e != nil {
		return e
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", res.StatusCode, b)
	}
	if action == "add" || action == "update" {
		var v map[string]any
		if json.Unmarshal(b, &v) == nil {
			if u, ok := v["url"].(string); ok {
				v["url"] = strings.TrimRight(*base, "/") + u
			}
			if u, ok := v["api"].(string); ok {
				v["api"] = strings.TrimRight(*base, "/") + u
			}
			b, _ = json.MarshalIndent(v, "", "  ")
		}
	}
	if len(b) > 0 {
		fmt.Println(string(b))
	}
	return nil
}
func envDefault(k, v string) string {
	if x := os.Getenv(k); x != "" {
		return x
	}
	return v
}
