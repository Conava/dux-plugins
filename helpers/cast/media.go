package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultMediaAppID = "CC1AD845"
	nsMedia           = "urn:x-cast:com.google.cast.media"
)

func isLink(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// remoteType names a link's media type from its extension, else from the
// server's answer; the receiver needs it to pick a player.
func remoteType(ctx context.Context, link string) string {
	if u, err := url.Parse(link); err == nil {
		if t := typeByExt(u.Path); t != "" {
			return t
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if req, err := http.NewRequestWithContext(ctx, http.MethodHead, link, nil); err == nil {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
			if t, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";"); t != "" && t != "application/octet-stream" {
				return t
			}
		}
	}
	return "video/mp4"
}

// sidecarSubtitle finds "movie.srt" or "movie.vtt" next to "movie.mkv".
func sidecarSubtitle(file string) string {
	base := strings.TrimSuffix(file, filepath.Ext(file))
	for _, ext := range []string{".vtt", ".srt", ".VTT", ".SRT"} {
		if _, err := os.Stat(base + ext); err == nil {
			return base + ext
		}
	}
	return ""
}

type mediaInfo struct {
	url, contentType, title string
	subURL, subLang         string
	artist, album, coverURL string // songs
}

func loadRequest(m mediaInfo, id int64, start float64) map[string]any {
	mi := map[string]any{
		"contentId":   m.url,
		"contentUrl":  m.url,
		"contentType": m.contentType,
		"streamType":  "BUFFERED",
		"metadata":    map[string]any{"metadataType": 0, "title": m.title},
	}
	if m.artist != "" || m.album != "" || m.coverURL != "" {
		md := map[string]any{"metadataType": 3, "title": m.title, "artist": m.artist, "albumName": m.album}
		if m.coverURL != "" {
			md["images"] = []map[string]any{{"url": m.coverURL}}
		}
		mi["metadata"] = md
	}
	load := map[string]any{"type": "LOAD", "requestId": id, "media": mi, "autoplay": true, "currentTime": start}
	if m.subURL != "" {
		mi["tracks"] = []map[string]any{{
			"trackId": 1, "type": "TEXT", "subtype": "SUBTITLES",
			"trackContentId": m.subURL, "trackContentType": "text/vtt",
			"language": m.subLang, "name": m.subLang,
		}}
		mi["textTrackStyle"] = map[string]any{"backgroundColor": "#00000000", "edgeType": "OUTLINE", "edgeColor": "#000000FF"}
		load["activeTrackIds"] = []int{1}
	}
	return load
}

// playerStatus is the last MEDIA_STATUS for the loaded media.
type playerStatus struct {
	SessionID   int64   `json:"mediaSessionId"`
	PlayerState string  `json:"playerState"`
	IdleReason  string  `json:"idleReason"`
	CurrentTime float64 `json:"currentTime"`
	Rate        float64 `json:"playbackRate"`
	Duration    float64
	Title       string
	at          time.Time
	follow      bool // take every new media session (the YouTube app starts one per video)
}

// update applies one media-namespace message. idle is the idle reason once
// the media stopped (FINISHED, CANCELLED, INTERRUPTED, ERROR).
func (s *playerStatus) update(p []byte) (idle string, err error) {
	var msg struct {
		Type   string
		Status []struct {
			playerStatus
			Media *struct {
				Duration float64
				Metadata struct{ Title string }
			}
		}
		DetailedErrorCode int `json:"detailedErrorCode"`
	}
	if err := json.Unmarshal(p, &msg); err != nil {
		return "", nil
	}
	switch msg.Type {
	case "LOAD_FAILED":
		return "ERROR", fmt.Errorf("the receiver could not load it (detailedErrorCode %d)", msg.DetailedErrorCode)
	case "MEDIA_STATUS":
	default:
		if msg.Type != "" {
			log.Printf("receiver: %s", p)
		}
		return "", nil
	}
	for _, ns := range msg.Status {
		if s.SessionID != 0 && ns.SessionID != s.SessionID && !s.follow {
			continue
		}
		dur, title := s.Duration, s.Title
		if ns.SessionID != s.SessionID {
			dur, title = 0, ""
		}
		if ns.Media != nil {
			if ns.Media.Duration > 0 {
				dur = ns.Media.Duration
			}
			if ns.Media.Metadata.Title != "" {
				title = ns.Media.Metadata.Title
			}
		}
		follow := s.follow
		*s = ns.playerStatus
		s.Duration, s.Title, s.at, s.follow = dur, title, time.Now(), follow
		if s.PlayerState == "IDLE" && s.IdleReason != "" {
			if s.IdleReason == "ERROR" {
				return "ERROR", fmt.Errorf("the receiver stopped playing it")
			}
			return s.IdleReason, nil
		}
	}
	return "", nil
}

// sourceIP is the local address the kernel would use to reach ip; a UDP
// "connect" sends nothing.
func sourceIP(ip string) (string, error) {
	c, err := net.Dial("udp", net.JoinHostPort(ip, "9"))
	if err != nil {
		return "", err
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String(), nil
}

func typeByExt(file string) string {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".mp4", ".m4v", ".mov":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".ts", ".m2ts":
		return "video/mp2t"
	case ".m4a", ".aac":
		return "audio/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".flac":
		return "audio/flac"
	case ".ogg", ".oga", ".opus":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".m3u8":
		return "application/x-mpegurl"
	case ".mpd":
		return "application/dash+xml"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	}
	return ""
}

// server serves only the files added to it, under a random path prefix, and
// only to the TV.
type server struct {
	fetched atomic.Bool // the TV has asked for something
	ln      net.Listener
	prefix  string
	tvIP    string
	mu      sync.Mutex
	routes  map[string]http.HandlerFunc
}

func newServer(addr, tvIP string) (*server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 16)
	rand.Read(b)
	s := &server{ln: ln, prefix: "/" + hex.EncodeToString(b) + "/", tvIP: tvIP, routes: map[string]http.HandlerFunc{}}
	go http.Serve(ln, s)
	return s, nil
}

func (s *server) close() { s.ln.Close() }

func (s *server) url(name string) string {
	return "http://" + s.ln.Addr().String() + s.prefix + name
}

func (s *server) add(name, file, contentType string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[s.prefix+name] = func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open(file)
		if err != nil {
			http.Error(w, "gone", http.StatusNotFound)
			return
		}
		defer f.Close()
		st, _ := f.Stat()
		w.Header().Set("Content-Type", contentType)
		http.ServeContent(w, r, name, st.ModTime(), f)
	}
	return s.url(name)
}

func (s *server) addBytes(name string, data []byte, contentType string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	mod := time.Now()
	s.routes[s.prefix+name] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		http.ServeContent(w, r, name, mod, strings.NewReader(string(data)))
	}
	return s.url(name)
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	debugf("http %s %s %s range=%q", host, r.Method, path.Base(r.URL.Path), r.Header.Get("Range"))
	if s.tvIP != "" && host != s.tvIP {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.fetched.Store(true)
	s.mu.Lock()
	h := s.routes[r.URL.Path]
	s.mu.Unlock()
	if h == nil {
		http.NotFound(w, r)
		return
	}
	// The receiver is a web page on another origin: text tracks, and media
	// played through its Shaka-based player, are fetched with CORS.
	hd := w.Header()
	hd.Set("Access-Control-Allow-Origin", "*")
	hd.Set("Access-Control-Allow-Headers", "Range, Content-Type")
	hd.Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges")
	switch r.Method {
	case http.MethodOptions:
		hd.Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet, http.MethodHead:
		h(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
