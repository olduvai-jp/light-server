package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		json.NewEncoder(w).Encode(v)
	}
}
func fail(w http.ResponseWriter, status int, msg string) {
	if status >= 500 {
		log.Printf("storage/request error: %s", msg)
	}
	reply(w, status, map[string]string{"error": msg})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
		w.ResponseWriter.WriteHeader(code)
	}
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	d.UseNumber()
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return fmt.Errorf("JSON本文は1つ必要")
	}
	return nil
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tracked := &statusWriter{ResponseWriter: w}
	w = tracked
	defer func() {
		if v := recover(); v != nil {
			log.Printf("panic: %v", v)
			if tracked.status == 0 {
				fail(w, 500, "内部エラー")
			}
		}
		log.Printf("%s %s status=%d", r.Method, r.URL.Path, tracked.status)
	}()
	if r.URL.Path == "/healthz" {
		reply(w, 200, map[string]string{"status": "ok"})
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "p" {
		s.static(w, r, parts[1])
		return
	}
	if len(parts) < 2 || parts[0] != "api" || parts[1] != "projects" {
		fail(w, 404, "見つかりません")
		return
	}
	if len(parts) == 2 {
		s.manage(w, r, "")
		return
	}
	id := parts[2]
	if !validName.MatchString(id) {
		fail(w, 400, "不正なプロジェクトID")
		return
	}
	if len(parts) == 3 {
		s.manage(w, r, id)
		return
	}
	p, release := s.acquire(id)
	if p == nil {
		fail(w, 404, "プロジェクトが見つかりません")
		return
	}
	defer release()
	if parts[3] == "files" {
		s.files(w, r, p, parts[4:])
		return
	}
	if parts[3] == "collections" {
		s.objects(w, r, p, parts[4:])
		return
	}
	fail(w, 404, "見つかりません")
}
func (s *Server) manage(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method == "POST" || r.Method == "PUT" {
		body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	// Wait for this project's in-flight transfers without holding the registry lock.
	if id != "" && r.Method == "DELETE" && r.URL.Query().Get("purge") == "true" {
		s.mu.RLock()
		old := s.projects[id]
		s.mu.RUnlock()
		if old != nil {
			old.life.Lock()
			defer old.life.Unlock()
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		p := s.projects[id]
		if p == nil {
			fail(w, 404, "未登録です")
			return
		}
		if old == nil || p.life != old.life {
			fail(w, 409, "登録状態が変更されました。再試行してください")
			return
		}
		p.db.Close()
		if e := os.RemoveAll(s.projectDir(id)); e != nil {
			fail(w, 500, e.Error())
			return
		}
		delete(s.projects, id)
		reply(w, 204, nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" && r.Method == "GET" {
		ps := []*Project{}
		for _, p := range s.projects {
			if p.Active || r.URL.Query().Get("all") == "true" {
				ps = append(ps, p)
			}
		}
		sort.Slice(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })
		reply(w, 200, ps)
		return
	}
	if (id == "" && r.Method == "POST") || (id != "" && r.Method == "PUT") {
		var q struct {
			ID  string `json:"id"`
			Dir string `json:"dir"`
		}
		if e := decode(w, r, &q); e != nil {
			fail(w, 400, e.Error())
			return
		}
		if id != "" {
			q.ID = id
		}
		if !validName.MatchString(q.ID) {
			fail(w, 400, "不正なID")
			return
		}
		dir, e := filepath.Abs(q.Dir)
		if q.Dir == "" || e != nil {
			fail(w, 400, "配信元が必要です")
			return
		}
		dir, e = filepath.EvalSymlinks(dir)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		info, e := os.Stat(dir)
		if e != nil || !info.IsDir() || within(dir, s.root) || within(s.root, dir) {
			fail(w, 400, "配信元は保存ルートと重ならないディレクトリを指定してください")
			return
		}
		old := s.projects[q.ID]
		if id == "" && old != nil && old.Active {
			fail(w, 409, "登録済みです")
			return
		}
		if id != "" && old == nil {
			fail(w, 404, "未登録です")
			return
		}
		p := &Project{ID: q.ID, Dir: dir, Active: true}
		if old != nil {
			p.db = old.db
			p.life = old.life
		} else if e = s.openDB(p); e != nil {
			fail(w, 500, e.Error())
			return
		}
		if e = s.save(p); e != nil {
			if old == nil {
				p.db.Close()
			}
			fail(w, 500, e.Error())
			return
		}
		s.projects[p.ID] = p
		status := 201
		if id != "" {
			status = 200
		}
		reply(w, status, map[string]any{"project": p, "url": "/p/" + p.ID + "/", "api": "/api/projects/" + p.ID})
		return
	}
	p := s.projects[id]
	if p == nil {
		fail(w, 404, "未登録です")
		return
	}
	if r.Method == "DELETE" {
		if r.URL.Query().Get("purge") == "true" {
			p.db.Close()
			if e := os.RemoveAll(s.projectDir(id)); e != nil {
				fail(w, 500, e.Error())
				return
			}
			delete(s.projects, id)
		} else {
			copy := *p
			copy.Active = false
			if e := s.save(&copy); e != nil {
				fail(w, 500, e.Error())
				return
			}
			s.projects[id] = &copy
		}
		reply(w, 204, nil)
		return
	}
	if r.Method == "GET" {
		reply(w, 200, p)
		return
	}
	fail(w, 405, "未対応のメソッド")
}
func (s *Server) static(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != "GET" && r.Method != "HEAD" {
		fail(w, 405, "未対応")
		return
	}
	p, release := s.acquire(id)
	if p == nil {
		fail(w, 404, "未登録です")
		return
	}
	defer release()
	prefix := "/p/" + id + "/"
	if r.URL.Path == "/p/"+id {
		http.Redirect(w, r, prefix, 301)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, prefix)
	path, e := filepath.EvalSymlinks(filepath.Join(p.Dir, rel))
	if e != nil || !within(p.Dir, path) || within(s.root, path) {
		fail(w, 404, "ファイルが見つかりません")
		return
	}
	info, e := os.Stat(path)
	if e != nil {
		fail(w, 404, "ファイルが見つかりません")
		return
	}
	if info.IsDir() {
		if !strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, r.URL.Path+"/", 301)
			return
		}
		path, e = filepath.EvalSymlinks(filepath.Join(path, "index.html"))
		if e != nil || !within(p.Dir, path) || within(s.root, path) {
			fail(w, 404, "index.htmlがありません")
			return
		}
	}
	f, e := os.Open(path)
	if e != nil {
		fail(w, 404, "ファイルが見つかりません")
		return
	}
	defer f.Close()
	info, e = f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		fail(w, 404, "通常ファイルではありません")
		return
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}
func page(r *http.Request) (int, int, error) {
	limit, offset := 100, 0
	var e error
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, e = strconv.Atoi(v)
		if e != nil {
			return 0, 0, e
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		offset, e = strconv.Atoi(v)
		if e != nil {
			return 0, 0, e
		}
	}
	if limit < 1 || limit > 1000 || offset < 0 {
		return 0, 0, fmt.Errorf("limitは1..1000、offsetは0以上")
	}
	return limit, offset, nil
}
func (s *Server) files(w http.ResponseWriter, r *http.Request, p *Project, tail []string) {
	dir := filepath.Join(s.projectDir(p.ID), "files")
	if len(tail) == 0 && r.Method == "GET" {
		limit, offset, e := page(r)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		es, e := os.ReadDir(dir)
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		items := []map[string]any{}
		for _, entry := range es {
			info, e := entry.Info()
			if e == nil && info.Mode().IsRegular() {
				items = append(items, map[string]any{"key": entry.Name(), "size": info.Size(), "updated_at": info.ModTime().UTC()})
			}
		}
		if offset > len(items) {
			offset = len(items)
		}
		end := offset + limit
		if end > len(items) {
			end = len(items)
		}
		reply(w, 200, map[string]any{"items": items[offset:end], "limit": limit, "offset": offset})
		return
	}
	if len(tail) != 1 || !validName.MatchString(tail[0]) {
		fail(w, 400, "不正なファイルキー")
		return
	}
	path := filepath.Join(dir, tail[0])
	switch r.Method {
	case "PUT":
		f, e := os.CreateTemp(s.projectDir(p.ID), ".upload-*")
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		defer os.Remove(f.Name())
		n, e := io.Copy(f, r.Body)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			fail(w, 400, "アップロード未完了: "+e.Error())
			return
		}
		if e = os.Rename(f.Name(), path); e != nil {
			fail(w, 500, e.Error())
			return
		}
		reply(w, 201, map[string]any{"key": tail[0], "size": n})
	case "GET", "HEAD":
		info, e := os.Lstat(path)
		if e != nil || !info.Mode().IsRegular() {
			fail(w, 404, "見つかりません")
			return
		}
		f, e := os.Open(path)
		if e != nil {
			fail(w, 404, "見つかりません")
			return
		}
		defer f.Close()
		http.ServeContent(w, r, tail[0], info.ModTime(), f)
	case "DELETE":
		if e := os.Remove(path); e != nil {
			fail(w, 404, "見つかりません")
			return
		}
		reply(w, 204, nil)
	default:
		fail(w, 405, "未対応")
	}
}
