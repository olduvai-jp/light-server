package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

type Project struct {
	ID     string `json:"id"`
	Dir    string `json:"dir"`
	Active bool   `json:"active"`
	db     *sql.DB
	life   *sync.RWMutex
}
type Server struct {
	root     string
	mu       sync.RWMutex
	projects map[string]*Project
	lock     *os.File
}

func NewServer(root string) (*Server, error) {
	a, e := filepath.Abs(root)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(filepath.Join(a, "projects"), 0700); e != nil {
		return nil, e
	}
	a, e = filepath.EvalSymlinks(a)
	if e != nil {
		return nil, e
	}
	l, e := os.OpenFile(filepath.Join(a, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = lockFile(l); e != nil {
		l.Close()
		return nil, fmt.Errorf("保存先は別プロセスが使用中: %w", e)
	}
	s := &Server{root: a, projects: map[string]*Project{}, lock: l}
	entries, e := os.ReadDir(filepath.Join(a, "projects"))
	if e != nil {
		s.Close()
		return nil, e
	}
	for _, entry := range entries {
		if !entry.IsDir() || !validName.MatchString(entry.Name()) {
			continue
		}
		b, e := os.ReadFile(filepath.Join(a, "projects", entry.Name(), "project.json"))
		if e != nil {
			s.Close()
			return nil, e
		}
		p := &Project{}
		if e = json.Unmarshal(b, p); e != nil || p.ID != entry.Name() {
			s.Close()
			return nil, fmt.Errorf("不正な登録情報: %s", entry.Name())
		}
		if e = s.openDB(p); e != nil {
			s.Close()
			return nil, e
		}
		s.projects[p.ID] = p
	}
	return s, nil
}
func (s *Server) projectDir(id string) string { return filepath.Join(s.root, "projects", id) }
func (s *Server) openDB(p *Project) error {
	p.life = &sync.RWMutex{}
	dir := s.projectDir(p.ID)
	if e := os.MkdirAll(filepath.Join(dir, "files"), 0700); e != nil {
		return e
	}
	tmp, _ := filepath.Glob(filepath.Join(dir, ".upload-*"))
	for _, f := range tmp {
		os.Remove(f)
	}
	db, e := sql.Open("sqlite", filepath.Join(dir, "data.sqlite"))
	if e != nil {
		return e
	}
	db.SetMaxOpenConns(1)
	_, e = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; CREATE TABLE IF NOT EXISTS objects(collection TEXT NOT NULL,id TEXT NOT NULL,data TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(collection,id));`)
	if e != nil {
		db.Close()
		return e
	}
	p.db = db
	return nil
}
func (s *Server) save(p *Project) error {
	b, e := json.MarshalIndent(p, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(s.projectDir(p.ID), ".project-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), filepath.Join(s.projectDir(p.ID), "project.json"))
}
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.projects {
		if p.db != nil {
			p.db.Close()
		}
	}
	if s.lock != nil {
		unlockFile(s.lock)
		return s.lock.Close()
	}
	return nil
}
func within(parent, child string) bool {
	r, e := filepath.Rel(parent, child)
	return e == nil && r != ".." && !filepath.IsAbs(r) && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// acquire protects project lifetime, without holding the registry during I/O.
// Project metadata is immutable; management replaces its pointer atomically.
func (s *Server) acquire(id string) (*Project, func()) {
	for {
		s.mu.RLock()
		p := s.projects[id]
		s.mu.RUnlock()
		if p == nil {
			return nil, nil
		}
		p.life.RLock()
		s.mu.RLock()
		current := s.projects[id]
		valid := current == p && p.Active
		s.mu.RUnlock()
		if valid {
			return p, p.life.RUnlock
		}
		p.life.RUnlock()
		if current == nil || !current.Active {
			return nil, nil
		}
	}
}
