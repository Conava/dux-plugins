package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// YouTube links play in the TV's own YouTube app, started the way pychromecast
// and catt do it (through the casttube library): ask the app for its screen
// id, get a lounge token for that screen from youtube.com, bind a remote
// control session and hand it the video. Playback is then controlled through
// the normal Cast media namespace.
const (
	youtubeAppID = "233637DE"
	nsYouTube    = "urn:x-cast:com.google.youtube.mdx"
	loungeBase   = "https://www.youtube.com/api/lounge/"
)

type youtubeVideo struct {
	id, list string
	start    int // seconds
}

var youtubeID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// parseYouTube recognizes watch, short, live, embed and youtu.be links.
func parseYouTube(link string) (youtubeVideo, bool) {
	u, err := url.Parse(link)
	if err != nil {
		return youtubeVideo{}, false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	q := u.Query()
	v := youtubeVideo{list: q.Get("list"), start: parseStart(q.Get("t"))}
	switch host {
	case "youtu.be":
		v.id = strings.Trim(u.Path, "/")
	case "youtube.com", "m.youtube.com", "music.youtube.com", "youtube-nocookie.com":
		v.id = q.Get("v")
		for _, prefix := range []string{"/shorts/", "/live/", "/embed/"} {
			if rest, ok := strings.CutPrefix(u.Path, prefix); ok {
				v.id = strings.Split(rest, "/")[0]
			}
		}
	default:
		return youtubeVideo{}, false
	}
	return v, youtubeID.MatchString(v.id)
}

// parseStart reads YouTube's t parameter: "90", "90s" or "1h2m3s".
func parseStart(t string) int {
	if n, err := strconv.Atoi(strings.TrimSuffix(t, "s")); err == nil {
		return n
	}
	total := 0
	for _, m := range regexp.MustCompile(`(\d+)([hms])`).FindAllStringSubmatch(t, -1) {
		n, _ := strconv.Atoi(m[1])
		total += n * map[string]int{"h": 3600, "m": 60, "s": 1}[m[2]]
	}
	return total
}

var (
	sidRe      = regexp.MustCompile(`"c","(.*?)","`)
	gsessionRe = regexp.MustCompile(`"S","(.*?)"]`)
)

// playOnYouTube starts the video in the YouTube app, whose transport the
// caller has already connected to.
func playOnYouTube(ctx context.Context, cast *castConn, transport string, v youtubeVideo) error {
	cast.send(transport, nsYouTube, map[string]any{"type": "getMdxSessionStatus"})
	raw, err := cast.await(ctx, nsYouTube, 10*time.Second, func(p []byte) bool { return typeOf(p) == "mdxSessionStatus" })
	if err != nil {
		return fmt.Errorf("YouTube app status: %w", err)
	}
	var status struct {
		Data struct {
			ScreenID string `json:"screenId"`
		}
	}
	json.Unmarshal(raw, &status)
	if status.Data.ScreenID == "" {
		return errors.New("YouTube app sent no screen id")
	}

	body, err := loungePost(ctx, "pairing/get_lounge_token_batch", nil, "", url.Values{"screen_ids": {status.Data.ScreenID}})
	if err != nil {
		return err
	}
	var tokens struct {
		Screens []struct {
			LoungeToken string `json:"loungeToken"`
		}
	}
	if json.Unmarshal(body, &tokens) != nil || len(tokens.Screens) == 0 || tokens.Screens[0].LoungeToken == "" {
		return fmt.Errorf("no lounge token in %.200s", body)
	}
	token := tokens.Screens[0].LoungeToken

	body, err = loungePost(ctx, "bc/bind", url.Values{"RID": {"0"}, "VER": {"8"}, "CVER": {"1"}}, token, url.Values{
		"device": {"REMOTE_CONTROL"}, "id": {remoteID()}, "name": {"Cast"}, "mdx-version": {"3"},
		"pairing_type": {"cast"}, "app": {"android-phone-13.14.55"},
	})
	if err != nil {
		return err
	}
	sid, gsession := sidRe.FindSubmatch(body), gsessionRe.FindSubmatch(body)
	if sid == nil || gsession == nil {
		return fmt.Errorf("unexpected bind response %.200s", body)
	}

	_, err = loungePost(ctx, "bc/bind", url.Values{"SID": {string(sid[1])}, "gsessionid": {string(gsession[1])},
		"RID": {"1"}, "VER": {"8"}, "CVER": {"1"}}, token, url.Values{
		"count": {"1"}, "req0__sc": {"setPlaylist"}, "req0_videoId": {v.id}, "req0_listId": {v.list},
		"req0_currentTime": {strconv.Itoa(v.start)}, "req0_currentIndex": {"-1"}, "req0_audioOnly": {"false"},
	})
	return err
}

func loungePost(ctx context.Context, path string, params url.Values, token string, form url.Values) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	u := loungeBase + path
	if params != nil {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Origin", "https://www.youtube.com/")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if token != "" {
		req.Header.Set("X-YouTube-LoungeId-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("youtube.com %s: %s", path, resp.Status)
	}
	return body, nil
}

// remoteID names this remote control for the TV's YouTube session.
func remoteID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 26)
	rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}
