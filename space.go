package space

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/hashicorp/go-hclog"
)

type ServerConfig struct {
	DataDir string
	Logger  hclog.Logger
}

type Server struct {
	conf    *ServerConfig
	handler http.Handler
}

func NewServer(conf ServerConfig) (*Server, error) {
	if conf.Logger == nil {
		conf.Logger = hclog.NewNullLogger()
	}

	s := &Server{
		conf: &conf,
	}

	h := s.defaultHandler()
	h = s.slashRemover(h)
	s.handler = h
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) defaultHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" || r.Method == "HEAD" {
			s.serveObject(w, r)
			return
		}
		if r.Method == "PUT" {
			s.saveObject(w, r)
			return
		}

		http.Error(w, "Error", http.StatusBadRequest)
		return
	})
}

func (s *Server) serveObject(w http.ResponseWriter, r *http.Request) {
	key, err := objKey(r)
	if err != nil {
		s.conf.Logger.Error("Invalid key", "error", err)
		http.Error(w, "Invalid key", http.StatusBadRequest)
		return
	}

	p := filepath.Join(s.conf.DataDir, objPathRel(key))
	s.conf.Logger.Debug("Open", "path", p)
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		s.conf.Logger.Error("Failed to open file", "path", p, "error", err)
		http.Error(w, "Error", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		s.conf.Logger.Error("Failed to get file info", "path", p, "error", err)
		http.Error(w, "Error", http.StatusInternalServerError)
		return
	}

	//w.Header().Set("cache-control", "public, max-age=1209600") // 2 Weeks = 1209600 Seconds
	//w.Header().Set("etag", "\""+md5Hash+"\"")

	if r.Method == "HEAD" {
		w.Header().Set("content-type", mime.TypeByExtension(filepath.Ext(p)))
		w.Header().Set("content-length", strconv.FormatInt(fi.Size(), 10))
		w.Header().Set("last-modified", fi.ModTime().UTC().Format(http.TimeFormat))
		w.WriteHeader(200)
		return
	}

	s.conf.Logger.Debug("Serve", "path", p)
	http.ServeContent(w, r, p, fi.ModTime(), f)
	return
}

func (s *Server) saveObject(w http.ResponseWriter, r *http.Request) {
	key, err := objKey(r)
	if err != nil {
		s.conf.Logger.Error("Invalid key", "error", err)
		http.Error(w, "Invalid key", http.StatusBadRequest)
		return
	}

	p := filepath.Join(s.conf.DataDir, objPathRel(key))
	dir := filepath.Dir(p)
	err = os.MkdirAll(dir, 0754)
	if err != nil {
		s.conf.Logger.Error("Failed to create dir", "dir", dir, "error", err)
		http.Error(w, "Error", http.StatusInternalServerError)
		return
	}

	s.conf.Logger.Debug("Write file", "path", p)
	_, err = writeFile(p, r.Body)
	if err != nil {
		s.conf.Logger.Error("Failed to write file", "path", p, "error", err)
		http.Error(w, "Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(200)
	return
}

func objPathRel(key string) string {
	return filepath.FromSlash(key)
}

var objKeyRE *regexp.Regexp = regexp.MustCompile(`^[a-zA-Z0-9/_.-]+$`)

func objKey(r *http.Request) (string, error) {
	keyOrig := r.URL.Path[1:]

	if !objKeyRE.Match([]byte(keyOrig)) {
		return "", fmt.Errorf("invalid key: %v", keyOrig)
	}

	key := path.Clean(keyOrig)
	if key != keyOrig ||
		key == "." ||
		key[0] == '/' ||
		strings.Contains(key, "..") {
		return "", fmt.Errorf("invalid key: %v", key)
	}

	key = strings.ToLower(key)
	return key, nil
}

func exists(p string) (bool, error) {
	_, err := os.Stat(p)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func isDir(p string) (bool, error) {
	fileInfo, err := os.Stat(p)
	if err != nil {
		return false, err
	}
	return fileInfo.IsDir(), nil
}

func writeFile(name string, r io.Reader) (int64, error) {
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, 0754)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, r)
}

func (s *Server) slashRemover(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Google treats URLs with trailing slash
		// and URLs without trailing slash separately and equally.
		// Prefer non-trailing slash URLs over trailing slash URLs.
		p := r.URL.Path
		if p != "/" && p[len(p)-1] == '/' {
			p = strings.TrimRight(p, "/")
			http.Redirect(w, r, p, 301)
			return
		}
		h.ServeHTTP(w, r)
	})
}
