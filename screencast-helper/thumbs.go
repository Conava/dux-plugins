package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// thumbSlots lets two thumbnails decode at once; a folder of videos asks for
// dozens.
var thumbSlots = make(chan struct{}, 2)

// thumbnail writes a JPEG of a video's first frame, cached by path, size and
// modification time, and reports where it is.
func (h *helper) thumbnail(file string, size int) {
	if size <= 0 || size > 1024 {
		size = 256
	}
	reply := func(out string) {
		h.emit(map[string]any{"event": "thumbnail", "path": file, "file": out})
	}
	st, err := os.Stat(file)
	if !filepath.IsAbs(file) || err != nil {
		reply("")
		return
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		reply("")
		return
	}
	sum := sha1.Sum(fmt.Appendf(nil, "%s|%d|%d|%d", file, size, st.Size(), st.ModTime().UnixNano()))
	out := filepath.Join(cache, "dms", "screencast", "thumbs", hex.EncodeToString(sum[:])+".jpg")
	if _, err := os.Stat(out); err == nil {
		reply(out)
		return
	}
	if need("thumbnails") != nil || os.MkdirAll(filepath.Dir(out), 0o700) != nil {
		reply("")
		return
	}

	thumbSlots <- struct{}{}
	defer func() { <-thumbSlots }()
	// A song's thumbnail is its cover: the embedded picture, else the folder's.
	if strings.HasPrefix(typeByExt(file), "audio/") {
		song := probeSong(file)
		switch cover := folderCover(file); {
		case song.cover != nil:
			out = strings.TrimSuffix(out, ".jpg") + "." + strings.TrimPrefix(song.coverType, "image/")
			if os.WriteFile(out, song.cover, 0o600) == nil {
				reply(out)
				return
			}
		case cover != "":
			reply(cover)
			return
		}
		reply("")
		return
	}
	v, err := probeVideo(file)
	if err != nil {
		reply("")
		return
	}
	// An audio branch only when there is audio: an unlinked sink would never
	// reach EOS and hold the pipeline open.
	desc := fmt.Sprintf("filesrc name=src ! decodebin name=d d. ! queue ! videoflip video-direction=auto ! videoconvert ! "+
		"videoscale ! video/x-raw,width=%d,pixel-aspect-ratio=1/1 ! jpegenc quality=85 snapshot=true ! filesink name=sink ", size)
	if v.audio {
		desc += "d. ! queue ! fakesink"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tmp := out + ".part"
	err = runGst(ctx, desc, map[string]map[string]string{"src": {"location": file}, "sink": {"location": tmp}}, nil)
	if err != nil || os.Rename(tmp, out) != nil {
		os.Remove(tmp)
		reply("")
		return
	}
	reply(out)
}
