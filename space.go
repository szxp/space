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
	"sync"

	"github.com/hashicorp/go-hclog"
)

type ServerConfig struct {
	SourceDir    string
	ThumbnailDir string
	AllowedExts  []string
	ImageResizer ImageResizer
	Logger       hclog.Logger
}

type Server struct {
	conf    *ServerConfig
	handler http.Handler

	thumbnailMutex    sync.Mutex
	pendingThumbnails map[string][]chan error
}

func NewServer(conf ServerConfig) (*Server, error) {
	if conf.Logger == nil {
		conf.Logger = hclog.NewNullLogger()
	}

	s := &Server{
		conf:              &conf,
		pendingThumbnails: make(map[string][]chan error),
	}

	mux := http.NewServeMux()
	mux.Handle("/source/", s.sourceHandler())
	mux.Handle("/thumbnail/", s.thumbnailHandler())

	h := http.Handler(mux)
	h = s.slashRemover(h)
	s.handler = h
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) thumbnailHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" || r.Method == "HEAD" {
			s.serveThumbnail(w, r)
			return
		}

		http.Error(w, "Error", http.StatusBadRequest)
		return
	})
}

func (s *Server) serveThumbnail(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(removePrefix(r.URL.Path, "/thumbnail/"))
	err := s.validateKey(key)
	if err != nil {
		s.conf.Logger.Error("Invalid key", "error", err)
		http.Error(w, "Invalid key", http.StatusBadRequest)
		return
	}

	f, err := s.openThumbnail(key)
	if err != nil && !os.IsNotExist(err) {
		s.conf.Logger.Error("Failed to open thumbnail", "key", key, "error", err)
		http.Error(w, "Error", http.StatusInternalServerError)
		return
	}
	if os.IsNotExist(err) {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		s.conf.Logger.Error("Failed to get file info", "key", key, "error", err)
		http.Error(w, "Error", http.StatusInternalServerError)
		return
	}

	//w.Header().Set("cache-control", "public, max-age=1209600") // 2 Weeks = 1209600 Seconds

	if r.Method == "HEAD" {
		w.Header().Set("content-type", mime.TypeByExtension(filepath.Ext(fi.Name())))
		w.Header().Set("content-length", strconv.FormatInt(fi.Size(), 10))
		w.Header().Set("last-modified", fi.ModTime().UTC().Format(http.TimeFormat))
		w.WriteHeader(200)
		return
	}

	http.ServeContent(w, r, "", fi.ModTime(), f)
	return
}

func (s *Server) thumbnailPath(key string) string {
	return filepath.Join(s.conf.ThumbnailDir, keyFilepath(key))
}

func (s *Server) openThumbnail(key string) (*os.File, error) {
	path := s.thumbnailPath(key)
	s.conf.Logger.Debug("Open thumbnail", "path", path)
	f, err := os.Open(path)
	if (err != nil && !os.IsNotExist(err)) || err == nil {
		return f, err
	}

	s.thumbnailMutex.Lock()
	ch := make(chan error, 1)
	s.pendingThumbnails[key] = append(s.pendingThumbnails[key], ch)
	if len(s.pendingThumbnails[key]) == 1 {
		go s.createThumbnail(key, path)
	}
	s.thumbnailMutex.Unlock()

	err = <-ch
	if err != nil {
		return nil, err
	}
	s.conf.Logger.Debug("Open thumbnail", "path", path)
	return os.Open(path)
}

func (s *Server) createThumbnail(key, path string) {
	s.conf.Logger.Debug("Stat thumbnail", "path", path)
	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		s.sendThumbnailResult(key, err)
		return
	}
	if err == nil {
		s.sendThumbnailResult(key, nil)
		return
	}

	src := s.sourcePath(key)
	s.conf.Logger.Debug("Stat source", "path", src)
	_, err = os.Stat(src)
	if err != nil {
		s.sendThumbnailResult(key, err)
		return
	}

	thDir := filepath.Dir(path)
	s.conf.Logger.Debug("MkDir", "path", thDir)
	err = os.MkdirAll(thDir, 0754)
	if err != nil {
		s.sendThumbnailResult(key, err)
		return
	}

	tc := thumbnailConfig{}
	err = tc.UnmarshalText(key)
	if err != nil {
		s.sendThumbnailResult(key, err)
		return
	}
	if tc.Width == 0 && tc.Height == 0 {
		tc.Width = 360
	}
	s.conf.Logger.Debug("Thumbnail config", "config", tc)

	s.conf.Logger.Debug("Create tmp file")
	tmpf, err := os.CreateTemp(filepath.Dir(path), "tmp")
	if err != nil {
		s.sendThumbnailResult(key, err)
		return
	}
	tmpPath := tmpf.Name()
	defer func() { os.Remove(tmpPath) }()
	tmpf.Close()

	s.conf.Logger.Debug("Create tmp thumbnail", "path", tmpPath)
	err = s.conf.ImageResizer.Resize(tmpPath, src, tc.Width, tc.Height, tc.Mode)
	if err != nil {
		s.sendThumbnailResult(key, err)
		return
	}

	s.conf.Logger.Debug("Rename tmp thumbnail", "old", tmpPath, "new", path)
	err = os.Rename(tmpPath, path)
	s.sendThumbnailResult(key, err)
	return
}

type thumbnailConfig struct {
	Width  uint64
	Height uint64
	Mode   int8
}

func (c *thumbnailConfig) UnmarshalText(s string) error {
	i := strings.LastIndex(s, "@@")
	if i == -1 {
		return nil
	}

	dot := strings.LastIndex(s, ".")
	if dot == -1 {
		dot = len(s)
	}

	opts := strings.Split(s[i+2:dot], "@")
	for _, opt := range opts {
		switch {
		case len(opt) > 0 && opt[0] == 'w':
			err := c.parseWidth(opt[1:])
			if err != nil {
				return err
			}
		case len(opt) > 0 && opt[0] == 'h':
			err := c.parseHeight(opt[1:])
			if err != nil {
				return err
			}
		case len(opt) > 0 && opt[0] == 'm':
			err := c.parseMode(opt[1:])
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *thumbnailConfig) parseWidth(s string) error {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return err
	}
	c.Width = n
	return nil
}

func (c *thumbnailConfig) parseHeight(s string) error {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return err
	}
	c.Height = n
	return nil
}

func (c *thumbnailConfig) parseMode(s string) error {
	if s == "f" {
		c.Mode = ResizeModeFit
		return nil
	} else if s == "c" {
		c.Mode = ResizeModeCover
		return nil
	} else if s == "s" {
		c.Mode = ResizeModeStretch
		return nil
	}
	return fmt.Errorf("invalid mode: %s", s)
}

func (s *Server) sendThumbnailResult(key string, err error) {
	s.thumbnailMutex.Lock()
	defer s.thumbnailMutex.Unlock()

	for _, ch := range s.pendingThumbnails[key] {
		if err != nil {
			ch <- err
		}
		close(ch)
	}
	s.pendingThumbnails[key] = s.pendingThumbnails[key][:0]
}

func (s *Server) sourceHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" || r.Method == "HEAD" {
			s.serveSource(w, r)
			return
		}
		if r.Method == "PUT" {
			s.saveSource(w, r)
			return
		}

		http.Error(w, "Error", http.StatusBadRequest)
		return
	})
}

func removePrefix(url, prefix string) string {
	return strings.Replace(url, prefix, "", 1)
}

func (s *Server) sourcePath(key string) string {
	i := strings.LastIndex(key, "@@")
	if i != -1 {
		ext := path.Ext(key)
		key = key[:i] + ext
	}
	return filepath.Join(s.conf.SourceDir, keyFilepath(key))
}

func (s *Server) serveSource(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(removePrefix(r.URL.Path, "/source/"))
	err := s.validateKey(key)
	if err != nil {
		s.conf.Logger.Error("Invalid key", "error", err)
		http.Error(w, "Invalid key", http.StatusBadRequest)
		return
	}

	p := s.sourcePath(key)
	s.conf.Logger.Debug("Open source", "path", p)
	f, err := os.Open(p)
	if err != nil && !os.IsNotExist(err) {
		s.conf.Logger.Error("Failed to open file", "path", p, "error", err)
		http.Error(w, "Error", http.StatusInternalServerError)
		return
	}
	if os.IsNotExist(err) {
		http.Error(w, "Not found", http.StatusNotFound)
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

func (s *Server) saveSource(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(removePrefix(r.URL.Path, "/source/"))
	err := s.validateKey(key)
	if err != nil {
		s.conf.Logger.Error("Invalid key", "error", err)
		http.Error(w, "Invalid key", http.StatusBadRequest)
		return
	}

	p := s.sourcePath(key)
	dir := filepath.Dir(p)
	err = os.MkdirAll(dir, 0754)
	if err != nil {
		s.conf.Logger.Error("Failed to create dir", "dir", dir, "error", err)
		http.Error(w, "Error", http.StatusInternalServerError)
		return
	}

	_, err = s.writeFile(p, r.Body)
	if err != nil {
		s.conf.Logger.Error("Failed to write file", "path", p, "error", err)
		http.Error(w, "Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(200)
	return
}

func keyFilepath(key string) string {
	return filepath.FromSlash(key)
}

var keyRE *regexp.Regexp = regexp.MustCompile(`^[a-zA-Z0-9./@-]+$`)

func (s *Server) validateKey(key string) error {
	if !keyRE.Match([]byte(key)) {
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

func (s *Server) writeFile(path string, r io.Reader) (int64, error) {
	s.conf.Logger.Debug("Write file", "path", path)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0754)
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
