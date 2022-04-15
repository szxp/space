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
	SourceDir             string
	ThumbnailDir          string
	AllowedExts           []string
	ImageResizer          ImageResizer
	DefaultThumbnailWidth uint64
	AllowedThumbnailSizes ThumbnailSizes
	AllowedHosts          []string
	ThumbnailMaxAge       int64
	Logger                hclog.Logger
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

	s.handler = http.Handler(mux)
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.isHostAllowed(r.Host) {
		s.conf.Logger.Error("Invalid host header", "host", r.Host)
		r.Close = true
		http.Error(w, "Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("x-frame-options", "deny")
	w.Header().Set("x-content-type-options", "nosniff")
	w.Header().Set("content-security-policy", "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self';")
	w.Header().Set("x-xss-protection", "0")
	w.Header().Set("strict-transport-security", "max-age=31536000; includeSubDomains")
	w.Header().Set("referrer-policy", "no-referrer")
	w.Header().Set("permissions-policy", "accelerometer=(), autoplay=(), camera=(), fullscreen=(self), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()")
	w.Header().Set("cross-origin-opener-policy", "same-origin")

	// Google treats URLs with trailing slash
	// and URLs without trailing slash separately and equally.
	// Prefer non-trailing slash URLs over trailing slash URLs.
	p := r.URL.Path
	if p != "/" && p[len(p)-1] == '/' {
		p = strings.TrimRight(p, "/")
		http.Redirect(w, r, p, 301)
		return
	}

	s.handler.ServeHTTP(w, r)
}

func (s *Server) isHostAllowed(host string) bool {
	for _, allowedHost := range s.conf.AllowedHosts {
		if allowedHost == host {
			return true
		}
	}
	return false
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
	th, err := s.parseThumbnail(r)
	if err != nil {
		s.conf.Logger.Error("Invalid thumbnail", "error", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	f, err := s.openThumbnail(th)
	if err != nil && !os.IsNotExist(err) {
		s.conf.Logger.Error("Failed to open thumbnail", "thumbnail", th, "error", err)
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
		s.conf.Logger.Error("Failed to get file info", "thumbnail", th, "error", err)
		http.Error(w, "Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("cache-control", "public, max-age="+strconv.FormatInt(s.conf.ThumbnailMaxAge, 10)+", immutable")

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

type ThumbnailSizes []*ThumbnailSize

func (ts *ThumbnailSizes) UnmarshalText(s string) error {
	for _, sizeStr := range strings.Split(s, ",") {
		size := &ThumbnailSize{}
		err := size.UnmarshalText(sizeStr)
		if err != nil {
			return err
		}
		*ts = append(*ts, size)
	}
	return nil
}

func (ts *ThumbnailSizes) IsValid(w, h uint64) bool {
	for _, v := range *ts {
		if v.Width == w && v.Height == h {
			return true
		}
	}
	return false
}

type ThumbnailSize struct {
	Width  uint64
	Height uint64
}

func (ts *ThumbnailSize) UnmarshalText(s string) error {
	a := strings.Split(s, "x")
	if len(a) != 2 {
		return fmt.Errorf("invalid size: %s", s)
	}

	if a[0] != "" {
		w, err := strconv.ParseUint(a[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid width: %s", s)
		}
		ts.Width = w
	}
	if a[1] != "" {
		h, err := strconv.ParseUint(a[1], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid height: %s", s)
		}
		ts.Height = h
	}
	return nil
}

type thumbnail struct {
	Key    string
	Width  uint64
	Height uint64
	Mode   int8
}

func (s *Server) parseThumbnail(r *http.Request) (*thumbnail, error) {
	th := &thumbnail{}

	key := removePrefix(r.URL.Path, "/thumbnail/")
	err := s.validateKey(key)
	if err != nil {
		return nil, err
	}
	th.Key = key

	q := r.URL.Query()

	w := q.Get("w")
	if w != "" {
		width, err := strconv.ParseUint(w, 10, 64)
		if err != nil {
			return nil, err
		}
		th.Width = width
	}

	h := q.Get("h")
	if h != "" {
		height, err := strconv.ParseUint(h, 10, 64)
		if err != nil {
			return nil, err
		}
		th.Height = height
	}

	m := q.Get("m")
	if m != "" {
		switch m {
		case "1":
			th.Mode = ResizeModeFit
		case "2":
			th.Mode = ResizeModeCover
		case "3":
			th.Mode = ResizeModeStretch
		default:
			return nil, fmt.Errorf("invalid mode: %s", m)
		}
	}

	if (th.Width != 0 || th.Height != 0) &&
		!s.conf.AllowedThumbnailSizes.IsValid(th.Width, th.Height) {
		return nil, fmt.Errorf("invalid size: %dx%d", th.Width, th.Height)
	}

	if th.Width == 0 && th.Height == 0 {
		th.Width = s.conf.DefaultThumbnailWidth
	}

	return th, nil
}

func (th *thumbnail) RelPath() string {
	dot := strings.LastIndex(th.Key, ".")
	return fmt.Sprintf("%s-w%d-h%d-m%d%s",
		th.Key[:dot],
		th.Width,
		th.Height,
		th.Mode,
		th.Key[dot:],
	)
}

func (s *Server) thumbnailPath(key string) string {
	return filepath.Join(s.conf.ThumbnailDir, keyFilepath(key))
}

func (s *Server) openThumbnail(th *thumbnail) (*os.File, error) {
	path := s.thumbnailPath(th.RelPath())
	s.conf.Logger.Debug("Open thumbnail", "path", path)
	f, err := os.Open(path)
	if (err != nil && !os.IsNotExist(err)) || err == nil {
		return f, err
	}

	s.thumbnailMutex.Lock()
	ch := make(chan error, 1)
	s.pendingThumbnails[th.Key] = append(s.pendingThumbnails[th.Key], ch)
	if len(s.pendingThumbnails[th.Key]) == 1 {
		go s.createThumbnail(th, path)
	}
	s.thumbnailMutex.Unlock()

	err = <-ch
	if err != nil {
		return nil, err
	}
	s.conf.Logger.Debug("Open thumbnail", "path", path)
	return os.Open(path)
}

func (s *Server) createThumbnail(th *thumbnail, path string) {
	s.conf.Logger.Debug("Stat thumbnail", "path", path)
	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		s.sendThumbnailResult(th.Key, err)
		return
	}
	if err == nil {
		s.sendThumbnailResult(th.Key, nil)
		return
	}

	src := s.sourcePath(th.Key)
	s.conf.Logger.Debug("Stat source", "path", src)
	_, err = os.Stat(src)
	if err != nil {
		s.sendThumbnailResult(th.Key, err)
		return
	}

	thDir := filepath.Dir(path)
	s.conf.Logger.Debug("MkDir", "path", thDir)
	err = os.MkdirAll(thDir, 0754)
	if err != nil {
		s.sendThumbnailResult(th.Key, err)
		return
	}

	s.conf.Logger.Debug("Create tmp file")
	tmpf, err := os.CreateTemp(filepath.Dir(path), "tmp")
	if err != nil {
		s.sendThumbnailResult(th.Key, err)
		return
	}
	tmpPath := tmpf.Name()
	defer func() { os.Remove(tmpPath) }()
	tmpf.Close()

	s.conf.Logger.Debug("Create tmp thumbnail", "path", tmpPath)
	err = s.conf.ImageResizer.Resize(tmpPath, src, th.Width, th.Height, th.Mode)
	if err != nil {
		s.sendThumbnailResult(th.Key, err)
		return
	}

	s.conf.Logger.Debug("Rename tmp thumbnail", "old", tmpPath, "new", path)
	err = os.Rename(tmpPath, path)
	s.sendThumbnailResult(th.Key, err)
	return
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

	w.Header().Set("cache-control", "public, max-age=1209600, immutable") // 2 Weeks = 1209600 Seconds

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

var keyRE *regexp.Regexp = regexp.MustCompile(`^[a-zA-Z0-9./-]+$`)

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

const (
	// Maximum values of height and width given, aspect ratio preserved.
	ResizeModeFit = 1

	// Minimum values of width and height given, aspect ratio preserved.
	// The image will be cut to fit it exactly.
	ResizeModeCover = 2

	// 	Width and height emphatically given, original aspect ratio ignored.
	ResizeModeStretch = 3
)

type ImageResizer interface {
	Resize(dst, src string, width, height uint64, mode int8) error
}
