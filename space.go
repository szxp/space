package space

import (
	"crypto/md5"
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
	SourceDir    string
	ThumbnailDir string
	AllowedExts  []string
	Logger       hclog.Logger
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

	mux := http.NewServeMux()
	mux.Handle("/source/", s.sourceHandler())
	//mux.Handle("/thumbnail/", s.thumbnailHandler())

	h := http.Handler(mux)
	h = s.slashRemover(h)
	s.handler = h
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) sourceHandler() http.Handler {
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

func removeSourcePrefix(url string) string {
	return strings.Replace(url, "/source/", "", 1)
}

func (s *Server) serveObject(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(removeSourcePrefix(r.URL.Path))
	err := s.validateKey(key)
	if err != nil {
		s.conf.Logger.Error("Invalid key", "error", err)
		http.Error(w, "Invalid key", http.StatusBadRequest)
		return
	}

	p := filepath.Join(s.conf.SourceDir, objPathRel(key))
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
	key := strings.TrimSpace(removeSourcePrefix(r.URL.Path))
	err := s.validateKey(key)
	if err != nil {
		s.conf.Logger.Error("Invalid key", "error", err)
		http.Error(w, "Invalid key", http.StatusBadRequest)
		return
	}

	p := filepath.Join(s.conf.SourceDir, objPathRel(key))
	dir := filepath.Dir(p)
	err = os.MkdirAll(dir, 0754)
	if err != nil {
		s.conf.Logger.Error("Failed to create dir", "dir", dir, "error", err)
		http.Error(w, "Error", http.StatusInternalServerError)
		return
	}

	_, err = s.writeFileMD5(p, r.Body)
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

var objKeyRE *regexp.Regexp = regexp.MustCompile(`^[a-zA-Z0-9/._-]+$`)

func (s *Server) validateKey(key string) error {
	if !objKeyRE.Match([]byte(key)) {
		return fmt.Errorf("invalid key: %v", key)
	}

	keyCopy := key
	key = path.Clean(keyCopy)
	if key != keyCopy ||
		key == "." ||
		key[0] == '/' ||
		strings.Contains(key, "..") {
		return fmt.Errorf("invalid key: %v", key)
	}

	ext := path.Ext(key)
	if ext == "" {
		return fmt.Errorf("no ext: %v", key)
	}

	validExt := false
	if len(s.conf.AllowedExts) > 0 {
		for _, e := range s.conf.AllowedExts {
			if ext == e {
				validExt = true
				break
			}
		}
	}
	if !validExt {
		return fmt.Errorf("invalid ext: %v", key)
	}

	return nil
}

func (s *Server) writeFileMD5(path string, r io.Reader) (int64, error) {
	s.conf.Logger.Debug("Write file", "path", path)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0754)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	h := md5.New()
	w := io.MultiWriter(f, h)
	n, err := io.Copy(w, r)
	if err != nil {
		return n, err
	}

	sum := fmt.Sprintf("%x", h.Sum(nil))
	pathMD5 := path + ".md5"
	s.conf.Logger.Debug("Write MD5 file", "path", pathMD5, "md5", sum)
	fmd5, err := os.OpenFile(pathMD5, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0754)
	if err != nil {
		return n, err
	}
	defer fmd5.Close()

	_, err = fmd5.Write([]byte(sum))
	if err != nil {
		return n, err
	}
	return n, nil
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
