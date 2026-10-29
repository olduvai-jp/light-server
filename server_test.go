package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLifecycle(t *testing.T) {
	root := t.TempDir()
	static := t.TempDir()
	os.WriteFile(filepath.Join(static, "index.html"), []byte("hello"), 0600)
	s, e := NewServer(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()
	call := func(method, path, body string, want int) string {
		t.Helper()
		r, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		res, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, b)
		}
		return string(b)
	}
	reg, _ := json.Marshal(map[string]string{"id": "a", "dir": static})
	call("POST", "/api/projects", string(reg), 201)
	reg, _ = json.Marshal(map[string]string{"id": "b", "dir": static})
	call("POST", "/api/projects", string(reg), 201)
	call("GET", "/p/a/", "", 200)
	base := "/api/projects/a/collections/reviews/objects"
	call("PUT", base+"/one", `{"score":3,"text":"100%_match","flag":true}`, 200)
	call("GET", "/api/projects/b/collections/reviews/objects/one", "", 404)
	v := call("POST", "/api/projects/a/collections/reviews/search", `{"filters":[{"field":"score","op":"eq","value":3}]}`, 200)
	if !strings.Contains(v, "one") {
		t.Fatal(v)
	}
	v = call("POST", "/api/projects/a/collections/reviews/search", `{"filters":[{"field":"score","op":"eq","value":"3"}]}`, 200)
	if strings.Contains(v, "one") {
		t.Fatal(v)
	}
	call("PUT", "/api/projects/a/files/test.bin", "abcdef", 201)
	r, _ := http.NewRequest("GET", ts.URL+"/api/projects/a/files/test.bin", nil)
	r.Header.Set("Range", "bytes=1-3")
	res, _ := http.DefaultClient.Do(r)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 206 || string(b) != "bcd" {
		t.Fatal(res.StatusCode, string(b))
	}
	call("DELETE", "/api/projects/a", "", 204)
	call("GET", base+"/one", "", 404)
	reg, _ = json.Marshal(map[string]string{"id": "a", "dir": static})
	call("POST", "/api/projects", string(reg), 201)
	call("GET", base+"/one", "", 200)
	call("DELETE", "/api/projects/a?purge=true", "", 204)
	if _, e := os.Stat(filepath.Join(static, "index.html")); e != nil {
		t.Fatal(e)
	}
}

func TestPersistenceAndSafety(t *testing.T) {
	root := t.TempDir()
	site := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600)
	os.Symlink(filepath.Join(outside, "secret"), filepath.Join(site, "leak"))
	s, e := NewServer(root)
	if e != nil {
		t.Fatal(e)
	}
	if other, e := NewServer(root); e == nil {
		other.Close()
		t.Fatal("double startup accepted")
	}
	ts := httptest.NewServer(s)
	request := func(method, path, body string, want int) string {
		t.Helper()
		r, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		res, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s %s = %d %s", method, path, res.StatusCode, b)
		}
		return string(b)
	}
	b, _ := json.Marshal(map[string]string{"id": "p", "dir": site})
	request("POST", "/api/projects", string(b), 201)
	request("GET", "/p/p/leak", "", 404)
	request("PUT", "/api/projects/p/files/%2e%2e", "x", 400)
	request("PUT", "/api/projects/p/collections/c/objects/o", `{"v":"a%_b","n":null,"b":true}`, 200)
	request("PUT", "/api/projects/p/collections/c/objects/bad", `[]`, 400)
	request("PUT", "/api/projects/p/collections/c/objects/bad", `{} {}`, 400)
	v := request("POST", "/api/projects/p/collections/c/search", `{"filters":[{"field":"v","op":"contains","value":"%_"}]}`, 200)
	if !strings.Contains(v, `"id":"o"`) {
		t.Fatal(v)
	}
	v = request("POST", "/api/projects/p/collections/c/search", `{"filters":[{"field":"missing","op":"eq","value":null}]}`, 200)
	if strings.Contains(v, `"id":"o"`) {
		t.Fatal(v)
	}
	request("PUT", "/api/projects/p/files/a.bin", "original", 201)
	ts.Close()
	s.Close()
	s, e = NewServer(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ts = httptest.NewServer(s)
	defer ts.Close()
	request("GET", "/api/projects/p/collections/c/objects/o", "", 200)
	if request("GET", "/api/projects/p/files/a.bin", "", 200) != "original" {
		t.Fatal("lost file")
	}
	request("HEAD", "/api/projects/p/files/a.bin", "", 200)
}

type interruptedBody struct{ done bool }

func (b *interruptedBody) Read(p []byte) (int, error) {
	if b.done {
		return 0, io.ErrUnexpectedEOF
	}
	b.done = true
	return copy(p, "partial"), nil
}
func TestInterruptedUploadKeepsOriginal(t *testing.T) {
	s, e := NewServer(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	site := t.TempDir()
	b, _ := json.Marshal(map[string]string{"id": "a", "dir": site})
	call := func(method, path string, body io.Reader) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, body)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/api/projects", strings.NewReader(string(b))); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	call("PUT", "/api/projects/a/files/x", strings.NewReader("original"))
	w := call("PUT", "/api/projects/a/files/x", &interruptedBody{})
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("GET", "/api/projects/a/files/x", nil)
	if w.Body.String() != "original" {
		t.Fatal(w.Body.String())
	}
	files, _ := filepath.Glob(filepath.Join(s.projectDir("a"), ".upload-*"))
	if len(files) != 0 {
		t.Fatal(files)
	}
}

type stalledBody struct {
	entered chan struct{}
	release chan struct{}
	once    bool
}

func (b *stalledBody) Read(p []byte) (int, error) {
	if !b.once {
		b.once = true
		close(b.entered)
		<-b.release
	}
	return 0, io.ErrUnexpectedEOF
}
func TestSlowUploadDoesNotBlockOtherProjects(t *testing.T) {
	s, e := NewServer(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	site := t.TempDir()
	os.WriteFile(filepath.Join(site, "index.html"), []byte("ok"), 0600)
	for _, id := range []string{"a", "b"} {
		body, _ := json.Marshal(map[string]string{"id": id, "dir": site})
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/api/projects", strings.NewReader(string(body))))
		if w.Code != 201 {
			t.Fatal(w.Body.String())
		}
	}
	body := &stalledBody{entered: make(chan struct{}), release: make(chan struct{})}
	uploadDone := make(chan struct{})
	go func() {
		defer close(uploadDone)
		s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PUT", "/api/projects/a/files/slow", body))
	}()
	<-body.entered
	defer func() { close(body.release); <-uploadDone }()
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/api/projects", nil))
		done <- w.Code
	}()
	select {
	case code := <-done:
		if code != 200 {
			t.Fatal(code)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("project list blocked by upload")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/p/b/", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
