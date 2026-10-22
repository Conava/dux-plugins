pragma ComponentBehavior: Bound

import QtQuick
import Qt.labs.folderlistmodel
import Quickshell
import Quickshell.Io
import qs.Common
import qs.DCommon.Widgets
import qs.Modals.FileBrowser
import qs.Modules.ControlCenter
import qs.Modules.ControlCenter.Widgets
import qs.Modules.Plugins
import qs.Modules.Settings.Widgets
import qs.Services
import qs.Widgets as ShellWidgets

// Owns cast-helper and the state every surface binds to. The helper
// runs only while a device list is on screen or a cast is going. Also hosts
// the player window, which needs a single instance.
PluginComponent {
    id: root

    property var popoutService: null

    readonly property string helperName: "cast-helper"
    readonly property string bundledHelper: Paths.strip(Paths.data) + "/cast/bin/" + helperName
    property string helperPath: ""
    property bool helperResolved: false
    readonly property string installGuide: "https://github.com/Conava/dux-plugins/blob/master/Cast/README.md#3-install-cast-helper"
    // Arch gets the helper from the AUR, which also installs the packages it needs.
    property bool onArch: false
    // Downloads the helper built for this plugin version from the repository's releases.
    readonly property string helperDownloadCommand: {
        const version = pluginService?.availablePlugins?.[pluginId]?.version ?? "";
        if (!version)
            return "";
        const url = "https://github.com/Conava/dux-plugins/releases/download/cast-v" + version + "/" + helperName + "-$(uname -m)";
        return "dms dl " + url + " --timeout 300 -o " + bundledHelper + " && chmod +x " + bundledHelper;
    }

    property var devices: []
    property var session: null
    property var missing: []
    // The check report: every package, what it's for, and whether it's there.
    property var deps: []
    property string distro: "arch"
    // Set while a mirror plays on both because sound couldn't be kept off this computer.
    property var soundWarning: null
    property var lastError: null
    property bool checked: false

    property var demand: ({})
    readonly property bool discoveryWanted: Object.keys(demand).length > 0
    property bool starting: false
    property string pendingDevice: ""
    // The TV loads local files from this port, so a firewall rule can allow it.
    readonly property int mediaPort: parseInt(pluginData.mediaPort ?? "47830") || 47830
    readonly property bool casting: session !== null
    readonly property bool wanted: discoveryWanted || casting || starting

    readonly property string favorite: pluginData.favoriteDevice ?? ""
    readonly property var favoriteDevice: devices.find(d => d.id === favorite) ?? null
    readonly property var sessionDevice: session ? (devices.find(d => d.id === session.device) ?? {
            "id": session.device,
            "name": session.name ?? ""
        }) : null
    // Where "Send to" and the player cast: the running cast, the favorite, or the only device found.
    readonly property var defaultDevice: sessionDevice ?? favoriteDevice ?? (devices.length === 1 ? devices[0] : null)
    // Files, links and shows go over Chromecast, so AirPlay-only devices can't take them.
    readonly property var mediaDevices: devices.filter(d => canMedia(d))
    readonly property var defaultMediaDevice: [sessionDevice, favoriteDevice].find(d => d && canMedia(d)) ?? (mediaDevices.length === 1 ? mediaDevices[0] : null)

    // Seconds per photo; 0 is manual, where photos change only on next and previous.
    readonly property int slideshowInterval: {
        const v = parseInt(pluginData.slideshowInterval ?? "8");
        return isNaN(v) ? 8 : v;
    }

    // What was sent with the last play or queue, index-aligned with session.queue.
    property var queueItems: []
    // The player's folder the running queue came from, "" for anything else.
    property string queueFolder: ""
    property bool queueFromLibrary: false

    readonly property bool isMedia: session?.kind === "media"
    readonly property var sessionQueue: isMedia ? (session.queue ?? []) : []
    readonly property int sessionIndex: session?.index ?? 0
    readonly property var currentEntry: sessionQueue[sessionIndex] ?? null
    readonly property var currentItem: queueItems[sessionIndex] ?? null
    readonly property string currentArtist: currentEntry?.artist ?? ""
    readonly property bool isLink: !!currentItem?.url
    readonly property string currentKind: currentEntry?.kind ?? (isLink ? "video" : "")
    readonly property string currentTitle: currentEntry?.title || session?.title || baseName(currentItem?.path ?? currentItem?.url ?? "")
    readonly property bool isQueue: sessionQueue.length > 1
    readonly property bool preparing: isMedia && (session.phase === "converting" || session.phase === "preparing" || currentEntry?.status === "preparing")
    readonly property real prepProgress: session?.progress ?? currentEntry?.progress ?? 0
    readonly property bool slideshowRunning: isMedia && currentKind === "image" && (session.interval ?? 0) > 0
    // A timed slideshow paused from anywhere (here, the island, the pill), as
    // opposed to one in manual mode; the helper tells the two apart.
    readonly property bool slideshowPaused: session?.paused ?? false
    readonly property bool slideshowManual: isQueue && (session.interval ?? 0) === 0 && !slideshowPaused
    // On a photo in manual mode the main button moves on instead of playing.
    readonly property bool manualPhoto: slideshowManual && currentKind === "image"
    readonly property bool mediaPlaying: currentKind === "image" ? slideshowRunning : isMedia && session.playerState === "PLAYING"
    readonly property bool hasTimeline: isMedia && currentKind !== "image" && (session.duration ?? 0) > 0
    // When the current queue item came up, for the slideshow countdown.
    property double indexSince: 0

    // The player's show: an ordered list of files from anywhere. Items are
    // {path, name, kind, missing}; missing marks a file that was moved or deleted.
    property bool playerOpen: false
    property var library: blankShow()
    property int librarySelect: -1
    property string playerDeviceId: ""
    readonly property var playerDevice: mediaDevices.find(d => d.id === playerDeviceId) ?? defaultMediaDevice
    // Id of the show the running queue came from.
    property string playingShowId: ""
    // The player shows the running show.
    readonly property bool showingLibrary: queueFromLibrary && isMedia && library.id === playingShowId
    // Another show is running while the player shows this one.
    readonly property bool otherShowPlaying: queueFromLibrary && isMedia && library.id !== playingShowId
    readonly property var playingShow: queueFromLibrary ? (recentShows.find(r => r.id === playingShowId) ?? null) : null
    // Queue index per path of the running show; the show can be edited while it runs.
    readonly property var queueIndexOf: {
        const map = {};
        if (showingLibrary)
            queueItems.forEach((item, i) => {
                const key = itemKey(item);
                if (!(key in map))
                    map[key] = i;
            });
        return map;
    }
    // Shows opened or played lately, saved or not, newest first:
    // {id, title, folder, saved, paths, index}.
    property var recentShows: []
    // Saved shows: name → list of paths.
    property var savedShows: ({})
    // Every device seen so far, id → {name, model}, so settings can offer one
    // that is switched off right now.
    property var knownDevices: ({})

    function rememberDevices(list) {
        let next = null;
        for (const d of list) {
            const known = knownDevices[d.id];
            if (known && known.name === d.name)
                continue;
            next = next ?? Object.assign({}, knownDevices);
            next[d.id] = {
                "name": d.name,
                "model": d.model ?? ""
            };
        }
        if (!next)
            return;
        knownDevices = next;
        pluginService?.savePluginState(pluginId, "knownDevices", next);
    }

    property var thumbs: ({})
    property var _thumbPending: ({})
    property var _queue: []
    property string _folderSelect: ""
    property string _folderPath: ""
    property bool _folderAppend: false
    // Playlist files waiting for their entries: show index to insert at, or the one playing.
    property var _playlistAt: ({})
    property string _expandPlaying: ""

    readonly property var imageExtensions: ["jpg", "jpeg", "png", "webp", "gif", "bmp"]
    readonly property var videoExtensions: ["mp4", "mkv", "webm", "m4v", "mov", "avi"]
    readonly property var audioExtensions: ["mp3", "m4a", "flac", "ogg", "opus", "wav", "aac"]
    readonly property var playlistExtensions: ["m3u", "m3u8", "pls"]
    // What the file pickers offer: every kind the player plays, plus playlist files.
    readonly property var pickerFilters: imageExtensions.concat(videoExtensions, audioExtensions, playlistExtensions).map(e => "*." + e)

    function setDemand(key, on) {
        if (!!demand[key] === on)
            return;
        const next = Object.assign({}, demand);
        if (on)
            next[key] = true;
        else
            delete next[key];
        demand = next;
    }

    function baseName(path) {
        return String(path).replace(/\/+$/, "").split("/").pop();
    }

    function kindOf(path) {
        const ext = String(path).split(".").pop().toLowerCase();
        if (imageExtensions.includes(ext))
            return "image";
        if (videoExtensions.includes(ext))
            return "video";
        if (audioExtensions.includes(ext))
            return "audio";
        return "";
    }

    function isPlaylist(path) {
        return playlistExtensions.includes(String(path).split(".").pop().toLowerCase());
    }

    function isUrl(path) {
        return /^[a-z][a-z0-9+.-]*:\/\//i.test(String(path));
    }

    // A queue or show item's identity: its path, or its link.
    function itemKey(item) {
        return item?.path ?? item?.url ?? "";
    }

    function fileUrl(path) {
        return path ? "file://" + path.split("/").map(encodeURIComponent).join("/") : "";
    }

    function protocolsOf(device) {
        return device?.protocols ?? [device?.protocol ?? "chromecast"];
    }

    function canMedia(device) {
        return protocolsOf(device).includes("chromecast");
    }

    // Both Chromecast and AirPlay: the user picks which one mirrors.
    function hasBothProtocols(device) {
        const protocols = protocolsOf(device);
        return protocols.includes("chromecast") && protocols.includes("airplay");
    }

    // How a device mirrors: its remembered choice, AirPlay when that's all it speaks, else Chromecast.
    function mirrorProtocol(device) {
        if (!canMedia(device))
            return "airplay";
        return hasBothProtocols(device) ? ((pluginData.mirrorProtocol ?? {})[device.id] ?? "chromecast") : "chromecast";
    }

    function setMirrorProtocol(deviceId, protocol) {
        const next = Object.assign({}, pluginData.mirrorProtocol ?? {});
        if (protocol === "airplay")
            next[deviceId] = "airplay";
        else
            delete next[deviceId];
        setSetting("mirrorProtocol", next);
    }

    function deviceIcon(device) {
        if (!device)
            return "cast";
        if (!canMedia(device))
            return "airplay";
        const model = device.model ?? "";
        if (/group/i.test(model))
            return "speaker_group";
        if (/hub|display/i.test(model))
            return "nest_display";
        if (/audio|speaker|mini/i.test(model))
            return "speaker";
        return "tv";
    }

    function clock(seconds) {
        const s = Math.floor(Math.max(0, seconds));
        const h = Math.floor(s / 3600);
        const mm = String(Math.floor(s % 3600 / 60)).padStart(h > 0 ? 2 : 1, "0");
        const ss = String(s % 60).padStart(2, "0");
        return h > 0 ? `${h}:${mm}:${ss}` : `${mm}:${ss}`;
    }

    // The receiver reports its position only on changes; count on from there.
    function positionAt(now) {
        if (!isMedia)
            return 0;
        const elapsed = session.playerState === "PLAYING" && session.positionAt > 0 ? (now - session.positionAt) / 1000 * (session.rate ?? 1) : 0;
        const pos = (session.position ?? 0) + Math.max(0, elapsed);
        return session.duration > 0 ? Math.min(pos, session.duration) : pos;
    }

    // Seconds until the slideshow moves on, or -1 when it doesn't.
    function countdownAt(now) {
        if (!slideshowRunning)
            return -1;
        return Math.max(0, session.interval - (now - indexSince) / 1000);
    }

    // Resolution and frame rate from the quality preset, or from the custom
    // values. A bitrate of 0 lets the helper pick one.
    // More pixels per second than 1080p at 30 fps outruns some devices; the UI
    // marks it experimental.
    readonly property bool experimentalQuality: {
        const [lines, fps] = quality();
        return (lines === 1080 ? 1920 * 1080 : 1280 * 720) * fps > 1920 * 1080 * 30;
    }

    function quality() {
        switch (pluginData.quality ?? "balanced") {
        case "smooth":
            return [1080, 60, 0];
        case "low":
            return [720, 30, 0];
        case "custom":
            return [parseInt(pluginData.resolution ?? "1080"), parseInt(pluginData.frameRate ?? "30"), (pluginData.bitrate ?? 0) * 1000];
        default:
            return [1080, 30, 0];
        }
    }

    function begin(deviceId) {
        lastError = null;
        starting = true;
        pendingDevice = deviceId;
    }

    // The portal's screen picker can't take input while a popout or the
    // Control Center holds the keyboard, so those close before it can open.
    signal portalMayOpen

    function startCast(deviceId) {
        const [lines, fps, kbps] = quality();
        const device = devices.find(d => d.id === deviceId) ?? {
            "id": deviceId
        };
        // Starting first keeps the helper wanted while the popout closes.
        begin(deviceId);
        portalMayOpen();
        soundWarning = null;
        queueItems = [];
        queueFolder = "";
        queueFromLibrary = false;
        send({
            "cmd": "start",
            "device": deviceId,
            "audio": pluginData.audioTarget ?? "tv",
            "width": lines === 720 ? 1280 : 1920,
            "height": lines,
            "fps": fps,
            "kbps": kbps,
            "codec": pluginData.codec ?? "auto",
            "delay": pluginData.latency ?? 200,
            "restoreToken": pluginService?.loadPluginState(pluginId, "restoreToken", "") ?? "",
            "protocol": mirrorProtocol(device),
            "airplayPorts": pluginData.airplayPorts ?? "47840-47849"
        });
    }

    // AirPlay asks for a PIN shown on the TV, or a password set on the receiver.
    function pair(code) {
        send({
            "cmd": "pair",
            "pin": code
        });
    }

    function setSetting(key, value) {
        pluginService?.savePluginData(pluginId, key, value);
    }

    // Plays one local file on the TV, which decodes the original itself.
    function playFile(deviceId, path) {
        if (isPlaylist(path)) {
            playPlaylist(deviceId, path);
            return;
        }
        begin(deviceId);
        queueItems = [
            {
                "path": path
            }
        ];
        queueFolder = "";
        queueFromLibrary = false;
        send({
            "cmd": "play",
            "device": deviceId,
            "path": path,
            "port": mediaPort,
            "shrinkPhotos": pluginData.shrinkPhotos ?? false,
            "convertVideos": pluginData.convertVideos ?? true
        });
    }

    // Plays a playlist file as a queue; the helper expands it. Asking for its
    // entries too keeps queueItems aligned with the session for covers and titles.
    function playPlaylist(deviceId, path) {
        begin(deviceId);
        queueItems = [
            {
                "path": path
            }
        ];
        queueFolder = "";
        queueFromLibrary = false;
        _expandPlaying = path;
        send({
            "cmd": "queue",
            "device": deviceId,
            "items": queueItems,
            "index": 0,
            "interval": slideshowInterval,
            "loop": pluginData.slideshowLoop ?? false,
            "shrinkPhotos": pluginData.shrinkPhotos ?? false,
            "convertVideos": pluginData.convertVideos ?? true,
            "port": mediaPort
        });
        send({
            "cmd": "playlist",
            "path": path
        });
    }

    // Plays a YouTube link in the TV's YouTube app, or a direct media link.
    function playLink(deviceId, link) {
        begin(deviceId);
        queueItems = [
            {
                "url": link
            }
        ];
        queueFolder = "";
        queueFromLibrary = false;
        send({
            "cmd": "play",
            "device": deviceId,
            "url": link
        });
    }

    // Starts the show as a queue on the device from a show index, leaving out missing files.
    function playLibrary(deviceId, index) {
        const available = library.items.filter(i => !i.missing);
        if (!deviceId || available.length === 0)
            return;
        const items = available.map(i => i.url ? {
                "url": i.path
            } : {
                "path": i.path
            });
        const start = Math.min(library.items.slice(0, Math.max(0, index)).filter(i => !i.missing).length, items.length - 1);
        begin(deviceId);
        queueItems = items;
        queueFolder = library.folder;
        queueFromLibrary = true;
        playingShowId = library.id;
        send({
            "cmd": "queue",
            "device": deviceId,
            "items": items,
            "index": start,
            "interval": slideshowInterval,
            "loop": pluginData.slideshowLoop ?? false,
            "shrinkPhotos": pluginData.shrinkPhotos ?? false,
            "convertVideos": pluginData.convertVideos ?? true,
            "port": mediaPort
        });
        rememberShow(library, start);
    }

    // Shows a show item now: jumps within the running queue, or starts it from there.
    function showNow(index) {
        const queueIndex = queueIndexOf[library.items[index]?.path];
        if (showingLibrary && queueIndex !== undefined) {
            mediaCommand("jump", {
                "index": queueIndex
            });
            return;
        }
        playLibrary(playerDevice?.id ?? "", index);
    }

    // Seconds per photo, or 0 for manual; applies to a running show right away.
    function setSlideshowInterval(seconds) {
        setSetting("slideshowInterval", String(seconds));
        if (showingLibrary)
            mediaCommand("slideshow", {
                "interval": seconds
            });
    }

    function mediaCommand(cmd, extra) {
        send(Object.assign({
            "cmd": cmd
        }, extra ?? {}));
    }

    function togglePlay() {
        if (manualPhoto) {
            mediaCommand("next");
            return;
        }
        // Pausing a timed show keeps its interval; the helper reports it as paused.
        if (currentKind === "image") {
            mediaCommand(slideshowPaused ? "resume" : "pause");
            return;
        }
        mediaCommand(mediaPlaying ? "pause" : "resume");
    }

    function skip(seconds) {
        mediaCommand("seek", {
            "position": Math.max(0, positionAt(Date.now()) + seconds)
        });
    }

    function moveTo(deviceId) {
        if (session?.kind === "mirror")
            portalMayOpen();
        send({
            "cmd": "move",
            "device": deviceId
        });
    }

    function stopCast() {
        starting = false;
        send({
            "cmd": "stop"
        });
    }

    // Toggle used by the Control Center tile and the bar pill: stop a running
    // cast, or start one to the favorite device. Returns false when there is
    // nothing to start, so the caller can show the device list instead.
    function toggleFavorite() {
        if (casting || starting) {
            stopCast();
            return true;
        }
        if (!favorite)
            return false;
        startCast(favorite);
        return true;
    }

    function setVolume(level) {
        send({
            "cmd": "volume",
            "level": level
        });
    }

    function setFavorite(device) {
        const unset = favorite === device.id;
        setSetting("favoriteDevice", unset ? "" : device.id);
        setSetting("favoriteName", unset ? "" : device.name);
    }

    function recheck() {
        send({
            "cmd": "check"
        });
    }

    function requestThumbnail(path) {
        if (!path || path in thumbs || _thumbPending[path])
            return;
        _thumbPending[path] = true;
        send({
            "cmd": "thumbnail",
            "path": path,
            "size": 256
        });
    }

    // Opens the player. With a path, starts a show from that file's folder with it selected.
    function openPlayer(path) {
        playerOpen = true;
        if (path)
            loadFolder(path.substring(0, path.lastIndexOf("/")), path, false);
    }

    function closePlayer() {
        rememberShow(library, library.id === playingShowId ? sessionIndex : librarySelect);
        playerOpen = false;
    }

    // A show item; links (from playlists) keep their address in path.
    function makeItem(path, title) {
        const url = isUrl(path);
        return {
            "path": path,
            "url": url,
            "name": title || (url ? path : baseName(path)),
            "kind": kindOf(path) || (url ? "audio" : ""),
            "missing": false
        };
    }

    function setItems(items) {
        library = Object.assign({}, library, {
            "items": items
        });
    }

    function blankShow() {
        return {
            "id": String(Date.now()) + Math.random().toString(36).slice(2, 6),
            "title": "",
            "folder": "",
            "saved": "",
            "items": []
        };
    }

    // Replaces the player's show, keeping the one it had in the recent shows.
    function switchShow(next, select) {
        rememberShow(library, library.id === playingShowId ? sessionIndex : librarySelect);
        library = next;
        librarySelect = select;
    }

    function newShow() {
        switchShow(blankShow(), -1);
    }

    // Fills the show from a folder, or adds the folder's files to it.
    function loadFolder(folder, selectPath, append) {
        _folderSelect = selectPath ?? "";
        _folderPath = folder;
        _folderAppend = !!append;
        folderModel.folder = fileUrl(folder);
    }

    function collectFolder() {
        const folder = _folderPath;
        const paths = [];
        for (let i = 0; i < folderModel.count; i++) {
            const path = folderModel.get(i, "filePath");
            if (kindOf(path))
                paths.push(path);
        }
        folderModel.folder = "";
        if (_folderAppend && library.items.length > 0) {
            addFiles(paths);
            return;
        }
        const items = paths.map(p => makeItem(p));
        const kept = items.map(i => i.path);
        // The running show's folder opens as that show.
        const playing = queueFromLibrary && isMedia && queueFolder === folder;
        switchShow(Object.assign(blankShow(), {
            "title": baseName(folder),
            "folder": folder,
            "items": items
        }, playing ? {
            "id": playingShowId
        } : {}), Math.max(0, kept.indexOf(playing ? (currentItem?.path ?? "") : _folderSelect)));
    }

    // Adds files at the end; playlist files are expanded by the helper and their
    // entries inserted where the playlist was added.
    function addFiles(paths) {
        const known = new Set(library.items.map(i => i.path));
        let added = [];
        for (const p of paths) {
            if (isPlaylist(p)) {
                _playlistAt[p] = library.items.length + added.length;
                send({
                    "cmd": "playlist",
                    "path": p
                });
            } else if (kindOf(p) && !known.has(p)) {
                added.push(makeItem(p));
            }
        }
        if (added.length === 0)
            return;
        const first = library.items.length === 0;
        library = Object.assign({}, library, {
            "title": first ? "" : library.title,
            "folder": first ? "" : library.folder,
            "items": library.items.concat(added)
        });
        if (first)
            librarySelect = 0;
    }

    function removeItem(index) {
        const items = library.items.slice();
        items.splice(index, 1);
        setItems(items);
        librarySelect = Math.min(librarySelect, items.length - 1);
    }

    // Moves an item; the selection stays on the same item.
    function moveItem(from, to) {
        const items = library.items.slice();
        to = Math.max(0, Math.min(to, items.length - 1));
        if (from === to || from < 0 || from >= items.length)
            return;
        const selectedPath = items[librarySelect]?.path;
        const [item] = items.splice(from, 1);
        items.splice(to, 0, item);
        setItems(items);
        librarySelect = items.findIndex(i => i.path === selectedPath);
    }

    function saveShow(name) {
        name = name.trim();
        if (!name || library.items.length === 0)
            return;
        const next = Object.assign({}, savedShows);
        next[name] = library.items.map(i => i.path);
        savedShows = next;
        pluginService?.savePluginState(pluginId, "shows", savedShows);
        library = Object.assign({}, library, {
            "title": name,
            "saved": name
        });
    }

    function deleteShow(name) {
        const next = Object.assign({}, savedShows);
        delete next[name];
        savedShows = next;
        pluginService?.savePluginState(pluginId, "shows", savedShows);
    }

    function openShow(name) {
        const paths = savedShows[name] ?? [];
        switchShow(Object.assign(blankShow(), {
            "title": name,
            "saved": name,
            "items": paths.map(p => makeItem(p))
        }), 0);
        markMissing();
    }

    function openRecent(entry) {
        if (!entry || entry.id === library.id)
            return;
        switchShow({
            "id": entry.id,
            "title": entry.title,
            "folder": entry.folder,
            "saved": entry.saved,
            "items": entry.paths.map(p => makeItem(p))
        }, entry.id === playingShowId ? sessionIndex : entry.index);
        markMissing();
    }

    // A name for a show without one: its first item, and how many follow.
    function showName(title, names) {
        if (title)
            return title;
        if (names.length <= 1)
            return names[0] ?? I18n.trFor("cast", "New show");
        return I18n.trFor("cast", "%1 and %2 more").arg(names[0]).arg(names.length - 1);
    }

    // A playlist's entries came back: play them, or put them into the show.
    function insertPlaylist(path, entries) {
        if (path === _expandPlaying) {
            _expandPlaying = "";
            queueItems = entries.map(e => e.path ? {
                    "path": e.path
                } : {
                    "url": e.url
                });
            return;
        }
        if (!(path in _playlistAt))
            return;
        const at = Math.min(_playlistAt[path], library.items.length);
        delete _playlistAt[path];
        const items = entries.map(e => makeItem(e.path ?? e.url ?? "", e.title ?? ""));
        const first = library.items.length === 0;
        const next = library.items.slice();
        next.splice(at, 0, ...items);
        library = Object.assign({}, library, {
            "title": first ? baseName(path).replace(/\.[^.]+$/, "") : library.title,
            "folder": first ? "" : library.folder,
            "items": next
        });
        if (first || librarySelect < 0)
            librarySelect = 0;
        markMissing();
    }

    // Marks files of the show that no longer exist, without taking them out.
    function markMissing() {
        const paths = library.items.filter(i => !i.url).map(i => i.path);
        if (paths.length === 0)
            return;
        Proc.runCommand("cast.checkFiles", ["stat", "--printf=%n\\n", "--"].concat(paths), stdout => {
            const found = new Set(stdout.split("\n"));
            root.setItems(root.library.items.map(i => Object.assign({}, i, {
                            "missing": !i.url && !found.has(i.path)
                        })));
        });
    }

    function rememberShow(show, index) {
        if (!show || show.items.length === 0)
            return;
        const entry = {
            "id": show.id,
            "title": show.title,
            "folder": show.folder,
            "saved": show.saved,
            "paths": show.items.map(i => i.path),
            "index": Math.max(0, index ?? 0)
        };
        recentShows = [entry].concat(recentShows.filter(r => r.id !== show.id)).slice(0, 8);
        pluginService?.savePluginState(pluginId, "recentShows", recentShows);
    }

    // A show ends with its cast: the player goes back to its start page, which
    // offers to continue it.
    function endShow() {
        if (library.id === playingShowId) {
            rememberShow(library, sessionIndex);
            library = blankShow();
            librarySelect = -1;
        }
        queueFromLibrary = false;
    }

    // Continues a recent show on the device where it stopped.
    function continueShow(entry) {
        openRecent(entry);
        playLibrary(playerDevice?.id ?? "", entry.index);
    }

    // Keeps where the running show is, for going back to it.
    function rememberPlayingIndex(index) {
        const at = recentShows.findIndex(r => r.id === playingShowId);
        if (at < 0)
            return;
        const next = recentShows.slice();
        next[at] = Object.assign({}, next[at], {
            "index": index
        });
        recentShows = next;
        pluginService?.savePluginState(pluginId, "recentShows", recentShows);
    }

    // Plain words for a helper feature id.
    function featureText(feature) {
        switch (feature) {
        case "discovery":
            return I18n.trFor("cast", "Device discovery");
        case "mirroring":
            return I18n.trFor("cast", "Screen mirroring");
        case "sound":
            return I18n.trFor("cast", "Audio streaming");
        case "tvOnlySound":
            return I18n.trFor("cast", "Audio routing");
        case "files":
            return I18n.trFor("cast", "Media playback");
        case "mkv":
            return I18n.trFor("cast", "MKV support");
        case "convert":
            return I18n.trFor("cast", "Format conversion");
        case "photos":
            return I18n.trFor("cast", "Photo compression");
        case "heic":
            return I18n.trFor("cast", "HEIC support");
        case "airplay":
            return I18n.trFor("cast", "AirPlay");
        case "thumbnails":
            return I18n.trFor("cast", "Video thumbnails");
        }
        return feature ?? "";
    }

    function featuresText(features) {
        return (features ?? []).map(f => featureText(f)).join(", ");
    }

    // Package names on this distro for dependency ids; one package can cover several ids.
    function packagesFor(ids) {
        const names = (ids ?? []).map(id => deps.find(d => d.id === id)?.package ?? id);
        return names.filter((n, i) => names.indexOf(n) === i);
    }

    // The install command for this distro family, or just the package names on
    // the rest (NixOS, Gentoo, Void, …), whose users know their package manager.
    readonly property bool knownDistro: ["arch", "debian", "fedora", "opensuse"].includes(distro)

    function installCommand(ids) {
        const packages = packagesFor((ids ?? []).filter(id => !deps.find(d => d.id === id)?.manual));
        if (packages.length === 0)
            return "";
        const list = packages.join(" ");
        let command;
        switch (distro) {
        case "arch":
            command = "sudo pacman -S --needed " + list;
            break;
        case "fedora":
            command = "sudo dnf install " + list;
            break;
        case "debian":
            command = "sudo apt install " + list;
            break;
        case "opensuse":
            command = "sudo zypper install " + list;
            break;
        default:
            return packages.join(" ");
        }
        // avahi counts as missing until its daemon runs.
        if (ids.includes("avahi"))
            command += " && sudo systemctl enable --now avahi-daemon";
        return command;
    }

    // A distro step beyond the package: what to do, and commands where there is one.
    function hintInfo(hint) {
        switch (hint) {
        case "rpmfusionVA":
            return {
                "text": I18n.trFor("cast", "Hardware video encoding needs Mesa from RPM Fusion"),
                "commands": [
                    {
                        "label": "AMD",
                        "command": "sudo dnf swap mesa-va-drivers mesa-va-drivers-freeworld"
                    },
                    {
                        "label": "Intel",
                        "command": "sudo dnf swap libva-intel-media-driver intel-media-driver --allowerasing"
                    }
                ]
            };
        case "packmanVA":
            return {
                "text": I18n.trFor("cast", "Hardware video encoding on AMD needs Mesa from Packman"),
                "commands": []
            };
        case "doubletakeAUR":
            return {
                "text": I18n.trFor("cast", "Install doubletake-bin from the AUR"),
                "commands": [
                    {
                        "label": "",
                        "command": "doubletake-bin"
                    }
                ]
            };
        case "doubletakeRelease":
            return {
                "text": I18n.trFor("cast", "Install doubletake from its releases page"),
                "commands": [
                    {
                        "label": "",
                        "command": "https://github.com/omarroth/doubletake/releases"
                    }
                ]
            };
        case "rpmfusionHeif":
            return {
                "text": I18n.trFor("cast", "HEIC photos need libheif-freeworld from RPM Fusion"),
                "commands": [
                    {
                        "label": "",
                        "command": "sudo dnf install libheif-freeworld"
                    }
                ]
            };
        }
        return null;
    }

    // The hints of the given dependency ids, each once.
    function hintsFor(ids) {
        const seen = [];
        for (const id of ids ?? []) {
            const hint = deps.find(d => d.id === id)?.hint;
            if (hint && !seen.includes(hint))
                seen.push(hint);
        }
        return seen.map(h => hintInfo(h)).filter(h => h);
    }

    function missingText(feature) {
        return I18n.trFor("cast", "%1 requires additional packages").arg(featureText(feature));
    }

    function missingHint(ids) {
        return I18n.trFor("cast", "Install %1").arg(packagesFor(ids).join(", "));
    }

    // Why a queue item failed.
    function itemErrorText(entry) {
        if (entry?.error === "missingDependency" && entry.feature)
            return missingText(entry.feature) + " · " + missingHint(entry.missing);
        return errorText(entry?.error ?? "");
    }

    // The headline for an error event; a missing package names the feature it's for.
    function errorTitle(err) {
        if (err?.code === "missingDependency" && err.feature)
            return missingText(err.feature);
        return errorText(err?.code);
    }

    function errorText(code) {
        switch (code) {
        case "missingDependency":
            return I18n.trFor("cast", "Additional packages required");
        case "portalDenied":
            return I18n.trFor("cast", "Screen sharing was cancelled");
        case "deviceLost":
            return I18n.trFor("cast", "Lost the connection to the cast device");
        case "deviceNotFound":
            return I18n.trFor("cast", "The cast device is no longer on the network");
        case "launchRefused":
            return I18n.trFor("cast", "The cast device refused the cast");
        case "authorizationTimeout":
            return I18n.trFor("cast", "The cast wasn't accepted on the device in time");
        case "encoderMissing":
            return I18n.trFor("cast", "No video encoder found for this device");
        case "pipelineFailed":
            return I18n.trFor("cast", "Screen capture stopped unexpectedly");
        case "unsupportedFile":
            return I18n.trFor("cast", "This file can't be played on the cast device");
        case "loadFailed":
            return I18n.trFor("cast", "The cast device couldn't play this file");
        case "receiverStopped":
            return I18n.trFor("cast", "The cast was ended on the device");
        case "serverUnreachable":
            return I18n.trFor("cast", "The cast device can't reach this computer");
        case "convertFailed":
            return I18n.trFor("cast", "Preparing the video for the device failed");
        case "portInUse":
            return I18n.trFor("cast", "The port for playing files is already in use");
        case "airplayFailed":
            return I18n.trFor("cast", "AirPlay connection failed");
        case "airplayNoMedia":
            return I18n.trFor("cast", "Files and links play over Chromecast only");
        case "youtubeFailed":
            return I18n.trFor("cast", "YouTube didn't start on the cast device");
        case "unsupportedLink":
            return I18n.trFor("cast", "Only http and https links can be played");
        case "busy":
            return I18n.trFor("cast", "Already casting");
        case "helperCrashed":
            return I18n.trFor("cast", "The cast helper stopped unexpectedly");
        case "avahi":
            return I18n.trFor("cast", "Device discovery is unavailable");
        default:
            return I18n.trFor("cast", "Casting failed");
        }
    }

    // A second line under the error: what to do about it, where there is something.
    function errorHint(err) {
        switch (err?.code) {
        case "missingDependency":
            return missingHint(err.missing);
        case "serverUnreachable":
            return I18n.trFor("cast", "Allow TCP port %1 in your firewall").arg(mediaPort);
        case "portInUse":
            return I18n.trFor("cast", "Pick another port in the settings");
        default:
            // The helper's own detail is English and stays in the log.
            return "";
        }
    }

    function send(msg) {
        _queue.push(JSON.stringify(msg) + "\n");
        if (helper.running)
            flush();
        else
            ensureRunning();
    }

    function flush() {
        const lines = _queue;
        _queue = [];
        for (const line of lines)
            helper.write(line);
    }

    function ensureRunning() {
        if (helper.running)
            return;
        if (!helperResolved || !helperPath) {
            console.info("cast: helper not started, path", helperResolved ? "not found" : "not resolved yet");
            return;
        }
        helper.stdinEnabled = true;
        helper.running = true;
    }

    // The player follows the device to the new item, unless the user is looking at another one.
    function followIndex(from, to) {
        const selectedPath = library.items[librarySelect]?.path;
        const fromPath = itemKey(queueItems[from]);
        if (librarySelect >= 0 && selectedPath !== fromPath)
            return;
        const index = library.items.findIndex(i => i.path === itemKey(queueItems[to]));
        if (index >= 0)
            librarySelect = index;
    }

    function handle(line) {
        let msg;
        try {
            msg = JSON.parse(line);
        } catch (e) {
            console.warn("cast: bad helper output:", line);
            return;
        }
        switch (msg.event) {
        case "state":
            {
                const next = msg.session ?? null;
                if (!next || next.index !== session?.index || next.device !== session?.device || next.interval !== session?.interval)
                    indexSince = Date.now();
                if (next && session && next.index !== session.index && queueFromLibrary)
                    rememberPlayingIndex(next.index ?? 0);
                if (next && queueFromLibrary && next.index !== session?.index)
                    followIndex(session?.index ?? -1, next.index ?? 0);
                if (!next && session && queueFromLibrary && !starting)
                    endShow();
                devices = msg.devices ?? [];
                rememberDevices(devices);
                session = next;
                if (!next)
                    soundWarning = null;
                if (session)
                    starting = false;
                return;
            }
        case "error":
            starting = false;
            lastError = msg;
            console.warn("cast:", msg.code, msg.detail ?? "");
            return;
        case "check":
            missing = msg.missing ?? [];
            deps = msg.deps ?? [];
            distro = msg.distro ?? "arch";
            checked = true;
            return;
        case "warning":
            if (msg.code === "soundOnBoth" || msg.code === "airplaySound")
                soundWarning = msg;
            return;
        case "restoreToken":
            pluginService?.savePluginState(pluginId, "restoreToken", msg.token ?? "");
            return;
        case "playlist":
            insertPlaylist(msg.path, msg.items ?? []);
            return;
        case "thumbnail":
            {
                const next = Object.assign({}, thumbs);
                next[msg.path] = fileUrl(String(msg.file ?? "").replace(/^~\//, Quickshell.env("HOME") + "/"));
                thumbs = next;
                delete _thumbPending[msg.path];
                return;
            }
        }
    }

    onDiscoveryWantedChanged: {
        if (discoveryWanted || helper.running)
            send({
                "cmd": "discover",
                "on": discoveryWanted
            });
    }

    onWantedChanged: {
        if (wanted) {
            idleStop.stop();
            ensureRunning();
            return;
        }
        idleStop.restart();
    }

    // Stops the helper a moment after nothing needs it, so a panel closing
    // just as a cast starts can't cut the helper off mid-command. Closing
    // stdin lets it clean up and exit by itself.
    Timer {
        id: idleStop
        interval: 2000
        onTriggered: {
            if (!root.wanted && helper.running)
                helper.stdinEnabled = false;
        }
    }

    onPlayerOpenChanged: setDemand("player", playerOpen)

    Component.onCompleted: {
        recentShows = pluginService?.loadPluginState(pluginId, "recentShows", []) ?? [];
        savedShows = pluginService?.loadPluginState(pluginId, "shows", {}) ?? {};
        knownDevices = pluginService?.loadPluginState(pluginId, "knownDevices", {}) ?? {};
        findHelper();
        Proc.runCommand("cast.findPacman", ["test", "-x", "/usr/bin/pacman"], (out, code) => root.onArch = code === 0);
    }

    function findHelper() {
        Proc.runCommand("cast.findHelper", ["sh", "-c", "command -v cast-helper || true"], (stdout, exitCode) => {
            const onPath = stdout.trim();
            if (onPath) {
                root.helperPath = onPath;
                root.helperResolved = true;
                root.ensureRunningIfWanted();
                return;
            }
            Proc.runCommand("cast.findBundled", ["test", "-x", root.bundledHelper], (out, code) => {
                root.helperPath = code === 0 ? root.bundledHelper : "";
                root.helperResolved = true;
                root.ensureRunningIfWanted();
            });
        });
    }

    function ensureRunningIfWanted() {
        if (wanted)
            ensureRunning();
    }

    FolderListModel {
        id: folderModel

        folder: ""
        showDirs: false
        showHidden: false
        caseSensitive: false
        sortField: FolderListModel.Name
        nameFilters: root.imageExtensions.concat(root.videoExtensions, root.audioExtensions).map(e => "*." + e)
        onStatusChanged: {
            if (status === FolderListModel.Ready && folder.toString() !== "")
                root.collectFolder();
        }
    }

    Process {
        id: helper

        command: [root.helperPath]
        stdinEnabled: true

        onStarted: {
            console.info("cast: helper started:", root.helperPath);
            root._thumbPending = {};
            root.send({
                "cmd": "check"
            });
            if (root.discoveryWanted)
                root.send({
                    "cmd": "discover",
                    "on": true
                });
            root.flush();
        }

        onExited: (exitCode, exitStatus) => {
            console.info("cast: helper exited with", exitCode);
            const wasCasting = root.casting || root.starting;
            root.session = null;
            root.starting = false;
            root.devices = [];
            root._queue = [];
            if (exitCode !== 0 && wasCasting)
                root.lastError = {
                    "code": "helperCrashed"
                };
            if (root.wanted && exitCode === 0)
                Qt.callLater(root.ensureRunning);
        }

        stdout: SplitParser {
            onRead: line => root.handle(line)
        }

        stderr: SplitParser {
            onRead: line => console.info("cast-helper:", line)
        }
    }

    LazyLoader {
        active: root.playerOpen

        PlayerWindow {
            service: root
        }
    }

    // AirPlay asks for its code before doubletake opens the screen picker, and
    // the popout has already closed by then; this small window asks instead.
    readonly property bool airplayPairing: session?.protocol === "airplay" && session?.phase === "pairing"

    LazyLoader {
        active: root.airplayPairing

        PairWindow {
            service: root
        }
    }

    // A command to copy, such as installing missing packages.
    component CommandRow: Row {
        id: commandRow

        property string command: ""
        // A short label in front, such as which hardware the command is for.
        property string label: ""

        visible: command !== ""
        height: visible ? implicitHeight : 0
        spacing: Theme.spacingS

        StyledText {
            id: commandLabel
            anchors.verticalCenter: parent.verticalCenter
            visible: commandRow.label !== ""
            text: commandRow.label
            font.pixelSize: Theme.fontSizeSmall
            font.weight: Theme.fontWeightMedium
            color: Theme.surfaceText
        }

        StyledText {
            anchors.verticalCenter: parent.verticalCenter
            width: commandRow.width - copyButton.width - commandRow.spacing - (commandLabel.visible ? commandLabel.width + commandRow.spacing : 0)
            text: commandRow.command
            isMonospace: true
            wrapMode: Text.Wrap
            font.pixelSize: Theme.fontSizeSmall
            color: Theme.surfaceText
        }

        DActionButton {
            id: copyButton
            anchors.verticalCenter: parent.verticalCenter
            iconName: "content_copy"
            iconSize: Theme.iconSizeSmall
            tooltipText: I18n.trFor("cast", "Copy")
            Accessible.name: tooltipText
            onClicked: Quickshell.execDetached(["dms", "cl", "copy", commandRow.command])
        }
    }

    // Extra steps a distro needs beyond installing a package, for the given dependency ids.
    component PackageHints: Column {
        id: hints

        property var service: null
        property var ids: []
        readonly property var list: service?.hintsFor(ids) ?? []

        visible: list.length > 0
        height: visible ? implicitHeight : 0
        spacing: Theme.spacingXS

        Repeater {
            model: hints.list

            Column {
                id: hint
                required property var modelData

                width: hints.width
                spacing: Theme.spacingXS

                StyledText {
                    width: parent.width
                    text: hint.modelData.text
                    wrapMode: Text.WordWrap
                    font.pixelSize: Theme.fontSizeSmall
                    color: Theme.surfaceVariantText
                }

                Repeater {
                    model: hint.modelData.commands

                    CommandRow {
                        required property var modelData

                        width: hint.width
                        label: modelData.label
                        command: modelData.command
                    }
                }
            }
        }
    }

    // A photo, or a video's thumbnail from the helper, with an icon until it's there.
    component Thumb: StyledRect {
        id: thumb

        required property var service
        property string path: ""
        property string kind: ""
        property bool missing: false
        property int sourceSize: 256

        readonly property string source: missing ? "" : kind === "image" ? service.fileUrl(path) : (service.thumbs[path] ?? "")

        color: Theme.surfaceContainerHighest
        clip: true

        function fetch() {
            if ((kind === "video" || kind === "audio") && !missing && !service.isUrl(path))
                service.requestThumbnail(path);
        }

        onPathChanged: fetch()
        onKindChanged: fetch()
        onMissingChanged: fetch()
        Component.onCompleted: fetch()

        Image {
            id: image
            anchors.fill: parent
            source: thumb.source
            sourceSize.width: thumb.sourceSize
            sourceSize.height: thumb.sourceSize
            asynchronous: true
            fillMode: Image.PreserveAspectCrop
            visible: status === Image.Ready
        }

        DIcon {
            anchors.centerIn: parent
            visible: image.status !== Image.Ready
            name: thumb.missing ? "broken_image" : thumb.kind === "video" ? "movie" : thumb.kind === "audio" ? "music_note" : "image"
            size: Math.min(Theme.iconSize, thumb.height * 0.6)
            color: thumb.missing ? Theme.error : Theme.surfaceVariantText
        }
    }

    component PairWindow: ShellWidgets.DFloatingWindow {
        id: pairWin

        required property var service
        readonly property bool password: service.session?.credential === "password"
        // Pair or Cancel answer the prompt; closing the window otherwise cancels the cast.
        property bool answered: false

        function submit() {
            if (codeField.text === "")
                return;
            answered = true;
            service.pair(codeField.text);
        }

        title: I18n.trFor("cast", "Pair with AirPlay")
        implicitWidth: Math.round(Theme.fontSizeMedium * 30)
        implicitHeight: pairLayout.implicitHeight + Theme.spacingL * 2
        visible: true
        onClosed: {
            if (!answered)
                service.stopCast();
        }

        FloatingWindowControls {
            id: pairControls
            targetWindow: pairWin
        }

        Column {
            id: pairLayout
            anchors.fill: parent
            anchors.margins: Theme.spacingL
            spacing: Theme.spacingM

            DWindowHeader {
                width: parent.width
                controls: pairControls
                title: I18n.trFor("cast", "Pair with %1").arg(pairWin.service.sessionDevice?.name ?? "")
                subtitle: pairWin.password ? I18n.trFor("cast", "Enter the AirPlay password") : I18n.trFor("cast", "Enter the code shown on the screen")
                titleAlignment: Text.AlignLeft
                onCloseRequested: pairWin.close()
            }

            DTextField {
                id: codeField
                width: parent.width
                placeholderText: pairWin.password ? I18n.trFor("cast", "Password") : I18n.trFor("cast", "Code")
                echoMode: pairWin.password ? TextInput.Password : TextInput.Normal
                validator: RegularExpressionValidator {
                    regularExpression: pairWin.password ? /.*/ : /\d{0,8}/
                }
                onAccepted: pairWin.submit()
                Component.onCompleted: forceActiveFocus()
            }

            Row {
                anchors.right: parent.right
                spacing: Theme.spacingS

                DButton {
                    text: I18n.trFor("cast", "Cancel")
                    backgroundColor: Theme.secondaryContainer
                    textColor: Theme.onSecondaryContainer
                    onClicked: {
                        pairWin.answered = true;
                        pairWin.service.stopCast();
                    }
                }

                DButton {
                    text: I18n.trFor("cast", "Pair")
                    enabled: codeField.text !== ""
                    onClicked: pairWin.submit()
                }
            }
        }
    }

    component PlayerWindow: ShellWidgets.DFloatingWindow {
        id: win

        required property var service

        readonly property var items: service.library.items
        readonly property bool empty: items.length === 0
        // The start page: saved shows and recent folders.
        property bool home: false
        readonly property bool showHome: empty || home
        readonly property int missingCount: items.filter(i => i.missing).length
        readonly property int selected: Math.min(Math.max(0, service.librarySelect), items.length - 1)
        readonly property var selectedItem: items[selected] ?? null
        readonly property var selectedEntry: entryFor(selectedItem)
        // A failed start, or the selected item failing for a missing package.
        readonly property var notice: {
            if (service.lastError)
                return service.lastError;
            if (selectedEntry?.status === "failed" && selectedEntry.error === "missingDependency")
                return {
                    "code": "missingDependency",
                    "feature": selectedEntry.feature,
                    "missing": selectedEntry.missing
                };
            return null;
        }
        // What the device shows: the running show's item, or the single file played from the panel.
        readonly property int onDevice: {
            const path = service.showingLibrary ? service.itemKey(service.currentItem) : "";
            return path ? items.findIndex(i => i.path === path) : -1;
        }
        readonly property var onDeviceItem: items[onDevice] ?? null
        readonly property var device: service.playerDevice
        readonly property string deviceName: device?.name ?? I18n.trFor("cast", "cast device")
        property bool gridMode: false
        property string pickMode: "files"

        // Ticks the countdown and the seek position, only while something moves on screen.
        property double now: Date.now()

        // The running queue's entry for a show item, live from the helper's state.
        function entryFor(item) {
            if (!item || !service.showingLibrary)
                return null;
            const index = service.queueIndexOf[item.path];
            return index === undefined ? null : (service.sessionQueue[index] ?? null);
        }

        function cachedAhead() {
            let n = 0;
            for (let i = service.sessionIndex + 1; i < service.sessionQueue.length && n < 9; i++) {
                if (service.sessionQueue[i].status !== "ready")
                    break;
                n++;
            }
            return n;
        }

        function pick(mode) {
            pickMode = mode;
            pickerLoader.active = true;
            pickerLoader.item?.open();
        }

        // From the start page, adding files or a folder begins a new show.
        function startShow(mode) {
            service.newShow();
            home = false;
            pick(mode);
        }

        title: I18n.trFor("cast", "Player")
        implicitWidth: Math.round(Theme.fontSizeMedium * 68)
        implicitHeight: heightWithin(Math.round(Theme.fontSizeMedium * 50), Theme.spacingXL * 2)
        minimumSize: Qt.size(Math.round(Theme.fontSizeMedium * 44), Math.round(Theme.fontSizeMedium * 34))
        visible: true
        onClosed: service.closePlayer()

        Timer {
            interval: 1000
            repeat: true
            running: win.visible && (win.service.slideshowRunning || (win.service.hasTimeline && win.service.mediaPlaying))
            onTriggered: win.now = Date.now()
        }

        FloatingWindowControls {
            id: windowControls
            targetWindow: win
        }

        Loader {
            id: pickerLoader
            active: false
            sourceComponent: FileBrowserSurfaceModal {
                browserTitle: {
                    switch (win.pickMode) {
                    case "folder":
                        return I18n.trFor("cast", "Add a folder");
                    case "playlist":
                        return I18n.trFor("cast", "Open a playlist");
                    }
                    return I18n.trFor("cast", "Add files");
                }
                bucket: "cast"
                folderMode: win.pickMode === "folder"
                multiple: win.pickMode === "files"
                filters: win.pickMode === "playlist" ? win.service.playlistExtensions.map(e => "*." + e) : win.service.pickerFilters
                onAccepted: paths => {
                    if (win.pickMode === "folder")
                        win.service.loadFolder(paths[0], "", true);
                    else
                        win.service.addFiles(paths);
                }
            }
        }

        DropArea {
            anchors.fill: parent
            onDropped: drop => {
                const paths = (drop.urls ?? []).map(u => decodeURIComponent(u.toString().replace("file://", "")));
                if (win.home)
                    win.service.newShow();
                win.home = false;
                win.service.addFiles(paths);
            }
        }

        Column {
            id: layout
            anchors.fill: parent
            spacing: 0

            DWindowHeader {
                id: header
                width: parent.width
                controls: windowControls
                title: win.showHome ? I18n.trFor("cast", "Player") : win.service.showName(win.service.library.title, win.items.map(i => i.name))
                subtitle: {
                    if (win.showHome)
                        return I18n.trFor("cast", "Shows");
                    const parts = [I18n.trFor("cast", "%1 items").arg(win.items.length)];
                    if (win.missingCount > 0)
                        parts.push(I18n.trFor("cast", "%1 not found").arg(win.missingCount));
                    return parts.join(" · ");
                }
                titleAlignment: Text.AlignLeft
                onCloseRequested: win.service.closePlayer()

                DActionButton {
                    visible: !win.empty
                    iconName: win.home ? "arrow_back" : "video_library"
                    tooltipText: win.home ? I18n.trFor("cast", "Back to the show") : I18n.trFor("cast", "Shows")
                    Accessible.name: tooltipText
                    onClicked: win.home = !win.home
                }

                DButton {
                    visible: !win.showHome
                    buttonHeight: Theme.buttonHeightXS
                    iconName: "add"
                    text: I18n.trFor("cast", "Add files")
                    backgroundColor: Theme.secondaryContainer
                    textColor: Theme.onSecondaryContainer
                    onClicked: win.pick("files")
                }

                DButton {
                    visible: !win.showHome
                    buttonHeight: Theme.buttonHeightXS
                    iconName: "create_new_folder"
                    text: I18n.trFor("cast", "Add a folder")
                    backgroundColor: Theme.secondaryContainer
                    textColor: Theme.onSecondaryContainer
                    onClicked: win.pick("folder")
                }

                DActionButton {
                    visible: !win.showHome
                    iconName: win.gridMode ? "view_carousel" : "grid_view"
                    tooltipText: win.gridMode ? I18n.trFor("cast", "Filmstrip") : I18n.trFor("cast", "Grid")
                    Accessible.name: tooltipText
                    onClicked: win.gridMode = !win.gridMode
                }

                DDropdown {
                    anchors.verticalCenter: parent.verticalCenter
                    compactMode: true
                    dropdownWidth: CcMetrics.rowDropdownWidth
                    alignPopupRight: true
                    emptyText: I18n.trFor("cast", "No cast devices")
                    options: win.service.mediaDevices.map(d => d.name)
                    currentValue: win.device?.name ?? ""
                    onValueChanged: value => {
                        const target = win.service.mediaDevices.find(d => d.name === value);
                        if (!target)
                            return;
                        win.service.playerDeviceId = target.id;
                        if (win.service.showingLibrary && target.id !== win.service.session.device)
                            win.service.moveTo(target.id);
                    }
                }
            }

            // Start page: a new show, saved shows and recent folders.
            DFlickable {
                visible: win.showHome
                width: parent.width
                height: parent.height - header.height
                contentHeight: homePage.implicitHeight + Theme.spacingXL * 2
                clip: true

                Column {
                    id: homePage
                    y: Theme.spacingXL
                    width: Math.min(parent.width - Theme.spacingL * 2, Theme.fontSizeMedium * 38)
                    x: (parent.width - width) / 2
                    spacing: Theme.groupedListGap

                    CcEmptyState {
                        width: parent.width
                        iconName: "photo_library"
                        title: I18n.trFor("cast", "Show something on %1").arg(win.deviceName)
                    }

                    Row {
                        anchors.horizontalCenter: parent.horizontalCenter
                        spacing: Theme.spacingM

                        DFab {
                            iconName: "add"
                            text: I18n.trFor("cast", "New show")
                            onClicked: win.startShow("files")
                        }

                        DButton {
                            anchors.verticalCenter: parent.verticalCenter
                            buttonHeight: Theme.buttonHeightM
                            iconName: "folder_open"
                            text: I18n.trFor("cast", "From a folder")
                            backgroundColor: Theme.secondaryContainer
                            textColor: Theme.onSecondaryContainer
                            onClicked: win.startShow("folder")
                        }

                        DButton {
                            anchors.verticalCenter: parent.verticalCenter
                            buttonHeight: Theme.buttonHeightM
                            iconName: "queue_music"
                            text: I18n.trFor("cast", "Open a playlist")
                            backgroundColor: Theme.secondaryContainer
                            textColor: Theme.onSecondaryContainer
                            onClicked: win.startShow("playlist")
                        }
                    }

                    Item {
                        width: parent.width
                        height: Theme.spacingL
                    }

                    CcGroup {
                        id: continueGroup

                        readonly property var last: win.service.recentShows[0] ?? null
                        readonly property bool playing: !!last && win.service.queueFromLibrary && win.service.isMedia && last.id === win.service.playingShowId

                        visible: !!last && (playing || last.index > 0)

                        CcListRow {
                            iconName: continueGroup.playing ? "cast" : "play_circle"
                            iconColor: Theme.primary
                            title: continueGroup.last ? win.service.showName(continueGroup.last.title, continueGroup.last.paths.map(p => win.service.baseName(p))) : ""
                            subtitle: {
                                const last = continueGroup.last;
                                if (!last)
                                    return "";
                                if (continueGroup.playing)
                                    return I18n.trFor("cast", "Playing on %1").arg(win.service.sessionDevice?.name ?? win.deviceName);
                                return I18n.trFor("cast", "Stopped at %1 of %2").arg(last.index + 1).arg(last.paths.length);
                            }

                            DButton {
                                anchors.verticalCenter: parent.verticalCenter
                                buttonHeight: Theme.buttonHeightXS
                                iconName: continueGroup.playing ? "arrow_forward" : "play_arrow"
                                text: continueGroup.playing ? I18n.trFor("cast", "Open") : I18n.trFor("cast", "Continue")
                                enabled: continueGroup.playing || !!win.device
                                onClicked: {
                                    if (continueGroup.playing)
                                        win.service.openRecent(continueGroup.last);
                                    else
                                        win.service.continueShow(continueGroup.last);
                                    win.home = false;
                                }
                            }
                        }
                    }

                    CcSectionLabel {
                        visible: savedRepeater.count > 0
                        text: I18n.trFor("cast", "Saved shows")
                    }

                    CcGroup {
                        visible: savedRepeater.count > 0

                        Repeater {
                            id: savedRepeater
                            model: Object.keys(win.service.savedShows).sort((a, b) => a.localeCompare(b))

                            CcListRow {
                                id: savedRow
                                required property var modelData

                                iconName: "video_library"
                                title: modelData
                                subtitle: I18n.trFor("cast", "%1 items").arg((win.service.savedShows[modelData] ?? []).length)
                                clickable: true
                                onClicked: {
                                    win.service.openShow(modelData);
                                    win.home = false;
                                }

                                DActionButton {
                                    anchors.verticalCenter: parent.verticalCenter
                                    iconName: "delete"
                                    tooltipText: I18n.trFor("cast", "Delete show")
                                    Accessible.name: tooltipText
                                    onClicked: win.service.deleteShow(savedRow.modelData)
                                }
                            }
                        }
                    }

                    CcSectionLabel {
                        visible: win.service.recentShows.length > 0
                        text: I18n.trFor("cast", "Recent shows")
                    }

                    CcGroup {
                        visible: win.service.recentShows.length > 0

                        Repeater {
                            model: win.service.recentShows

                            CcListRow {
                                required property var modelData
                                readonly property bool playing: modelData.id === win.service.playingShowId && win.service.queueFromLibrary && win.service.isMedia

                                iconName: playing ? "cast" : modelData.folder ? "folder" : modelData.saved ? "video_library" : "playlist_play"
                                iconColor: playing ? Theme.primary : Theme.surfaceVariantText
                                title: win.service.showName(modelData.title, modelData.paths.map(p => win.service.baseName(p)))
                                subtitle: {
                                    if (playing)
                                        return I18n.trFor("cast", "%1 items · playing").arg(modelData.paths.length);
                                    if (modelData.index > 0)
                                        return I18n.trFor("cast", "%1 items · stopped at %2").arg(modelData.paths.length).arg(modelData.index + 1);
                                    return I18n.trFor("cast", "%1 items").arg(modelData.paths.length);
                                }
                                showChevron: true
                                clickable: true
                                onClicked: {
                                    win.service.openRecent(modelData);
                                    win.home = false;
                                }
                            }
                        }
                    }
                }
            }

            // Grid of the whole show; drag a tile to reorder.
            DGridView {
                id: grid
                visible: !win.showHome && win.gridMode
                width: parent.width - Theme.windowInset * 2
                x: Theme.windowInset
                height: parent.height - header.height - Theme.windowInset
                clip: true
                interactive: !gridDragging
                property bool gridDragging: false
                readonly property int columns: Math.max(3, Math.floor(width / (Theme.fontSizeMedium * 11)))
                cellWidth: width / columns
                cellHeight: cellWidth * 0.75 + Theme.fontSizeSmall + Theme.spacingS
                model: win.gridMode ? win.items : []

                delegate: Item {
                    id: cell
                    required property var modelData
                    required property int index

                    width: grid.cellWidth
                    height: grid.cellHeight
                    z: cellMouse.drag.active ? 1 : 0

                    Thumb {
                        id: cellThumb
                        service: win.service
                        path: cell.modelData.path
                        kind: cell.modelData.kind
                        missing: cell.modelData.missing
                        x: Theme.spacingXS
                        width: parent.width - Theme.spacingXS * 2
                        height: width * 0.66
                        radius: Theme.cornerRadiusM
                        border.width: cell.index === win.selected || cell.index === win.onDevice ? Theme.focusRingWidth : 0
                        border.color: cell.index === win.onDevice ? Theme.primary : Theme.surfaceText
                    }

                    StyledText {
                        anchors.top: cellThumb.bottom
                        anchors.topMargin: Theme.spacingXS
                        x: Theme.spacingXS
                        width: cellThumb.width
                        text: cell.modelData.name
                        elide: Text.ElideRight
                        font.pixelSize: Theme.fontSizeSmall
                        color: cell.modelData.missing ? Theme.error : Theme.surfaceVariantText
                    }

                    MouseArea {
                        id: cellMouse
                        anchors.fill: parent
                        cursorShape: drag.active ? Qt.ClosedHandCursor : Qt.PointingHandCursor
                        drag.target: cell
                        preventStealing: true
                        onPressed: grid.gridDragging = true
                        onClicked: {
                            win.service.librarySelect = cell.index;
                            win.gridMode = false;
                        }
                        onReleased: {
                            grid.gridDragging = false;
                            const column = Math.max(0, Math.min(grid.columns - 1, Math.round(cell.x / grid.cellWidth)));
                            const to = Math.round(cell.y / grid.cellHeight) * grid.columns + column;
                            if (to !== cell.index)
                                win.service.moveItem(cell.index, to);
                            grid.forceLayout();
                        }
                    }
                }
            }

            // Stage, side panel, transport and filmstrip.
            Item {
                visible: !win.showHome && !win.gridMode
                width: parent.width
                height: parent.height - header.height

                Row {
                    id: top
                    x: Theme.windowInset
                    width: parent.width - Theme.windowInset * 2
                    height: parent.height - transport.height - strip.height - Theme.spacingM * 3
                    spacing: Theme.spacingL

                    // The big preview follows the selection; an inset keeps showing what the room sees.
                    Thumb {
                        id: stage
                        service: win.service
                        width: parent.width - side.width - parent.spacing
                        height: parent.height
                        radius: Theme.cornerRadiusXL
                        path: win.selectedItem?.path ?? ""
                        kind: win.selectedItem?.kind ?? ""
                        missing: win.selectedItem?.missing ?? false
                        sourceSize: 1600

                        readonly property bool isOnDevice: win.selected === win.onDevice

                        StyledRect {
                            visible: stage.isOnDevice
                            x: Theme.spacingM
                            y: Theme.spacingM
                            width: tagRow.implicitWidth + Theme.spacingM * 2
                            height: Theme.buttonHeightXS
                            radius: height / 2
                            color: Theme.primaryContainer

                            Row {
                                id: tagRow
                                anchors.centerIn: parent
                                spacing: Theme.spacingXS

                                DIcon {
                                    anchors.verticalCenter: parent.verticalCenter
                                    name: "cast"
                                    size: Theme.iconSizeSmall
                                    color: Theme.onPrimaryContainer
                                }

                                StyledText {
                                    anchors.verticalCenter: parent.verticalCenter
                                    text: I18n.trFor("cast", "On %1").arg(win.deviceName)
                                    font.pixelSize: Theme.fontSizeSmall
                                    font.weight: Theme.fontWeightMedium
                                    color: Theme.onPrimaryContainer
                                }
                            }
                        }

                        Thumb {
                            visible: !stage.isOnDevice && !!win.onDeviceItem
                            service: win.service
                            anchors.right: parent.right
                            anchors.top: parent.top
                            anchors.margins: Theme.spacingM
                            width: parent.width / 4
                            height: width * 0.62
                            radius: Theme.cornerRadiusL
                            border.width: Theme.focusRingWidth
                            border.color: Theme.primary
                            path: win.onDeviceItem?.path ?? ""
                            kind: win.onDeviceItem?.kind ?? ""
                        }

                        StyledRect {
                            anchors.left: parent.left
                            anchors.right: parent.right
                            anchors.bottom: parent.bottom
                            height: metaRow.implicitHeight + Theme.spacingM * 2
                            color: CcMetrics.rowColor

                            Row {
                                id: metaRow
                                anchors.fill: parent
                                anchors.margins: Theme.spacingM
                                spacing: Theme.spacingM

                                Column {
                                    width: parent.width - (showNow.visible ? showNow.width + parent.spacing : 0)
                                    anchors.verticalCenter: parent.verticalCenter

                                    StyledText {
                                        width: parent.width
                                        text: win.selectedEntry?.title || (win.selectedItem?.name ?? "")
                                        elide: Text.ElideMiddle
                                        font.pixelSize: Theme.fontSizeMedium
                                        font.weight: Theme.fontWeightMedium
                                        color: Theme.surfaceText
                                    }

                                    StyledText {
                                        width: parent.width
                                        text: {
                                            const pos = I18n.trFor("cast", "%1 of %2").arg(win.selected + 1).arg(win.items.length);
                                            const entry = win.selectedEntry;
                                            if (win.selectedItem?.missing)
                                                return pos + " · " + I18n.trFor("cast", "Not found");
                                            if (entry?.status === "failed")
                                                return pos + " · " + win.service.itemErrorText(entry);
                                            if (entry?.status === "preparing")
                                                return pos + " · " + I18n.trFor("cast", "Preparing... %1%").arg(Math.round((entry.progress ?? 0) * 100));
                                            if (win.selectedEntry?.artist)
                                                return pos + " · " + win.selectedEntry.artist;
                                            return pos;
                                        }
                                        elide: Text.ElideRight
                                        font.pixelSize: Theme.fontSizeSmall
                                        color: win.selectedItem?.missing ? Theme.error : Theme.surfaceVariantText
                                    }
                                }

                                DButton {
                                    id: showNow
                                    visible: !stage.isOnDevice && !!win.device && !(win.selectedItem?.missing ?? true)
                                    anchors.verticalCenter: parent.verticalCenter
                                    iconName: "cast"
                                    text: win.service.showingLibrary ? I18n.trFor("cast", "Show now") : I18n.trFor("cast", "Start here")
                                    onClicked: win.service.showNow(win.selected)
                                }
                            }
                        }
                    }

                    DFlickable {
                        id: side
                        width: Math.round(Theme.fontSizeMedium * 21)
                        height: parent.height
                        contentHeight: sideColumn.implicitHeight
                        clip: true

                        Column {
                            id: sideColumn
                            width: parent.width
                            spacing: Theme.groupedListGap

                            SettingsGroup {
                                visible: !!win.notice

                                SettingsRow {
                                    iconName: "error"
                                    iconColor: Theme.error
                                    title: win.notice ? win.service.errorTitle(win.notice) : ""
                                    subtitle: win.notice ? win.service.errorHint(win.notice) : ""

                                    body: Column {
                                        width: parent.width
                                        spacing: Theme.spacingS

                                        CommandRow {
                                            width: parent.width
                                            command: win.notice?.code === "missingDependency" ? win.service.installCommand(win.notice.missing) : ""
                                        }

                                        PackageHints {
                                            width: parent.width
                                            service: win.service
                                            ids: win.notice?.code === "missingDependency" ? (win.notice.missing ?? []) : []
                                        }
                                    }
                                }
                            }

                            SettingsGroup {
                                visible: win.service.otherShowPlaying

                                SettingsRow {
                                    iconName: "cast"
                                    title: I18n.trFor("cast", "Playing on %1").arg(win.service.sessionDevice?.name ?? win.deviceName)
                                    subtitle: win.service.playingShow ? win.service.showName(win.service.playingShow.title, win.service.playingShow.paths.map(p => win.service.baseName(p))) : ""

                                    body: DButton {
                                        buttonHeight: Theme.buttonHeightXS
                                        iconName: "arrow_back"
                                        text: I18n.trFor("cast", "Back to the running show")
                                        backgroundColor: Theme.secondaryContainer
                                        textColor: Theme.onSecondaryContainer
                                        onClicked: win.service.openRecent(win.service.playingShow)
                                    }
                                }
                            }

                            SettingsSectionLabel {
                                text: I18n.trFor("cast", "Show")
                                topGap: win.notice || win.service.otherShowPlaying ? SettingsMetrics.sectionLabelTopGap : 0
                            }

                            SettingsGroup {
                                SettingsRow {
                                    body: Row {
                                        width: parent.width
                                        spacing: Theme.spacingS

                                        DTextField {
                                            id: nameField
                                            width: parent.width - saveButton.width - parent.spacing
                                            placeholderText: I18n.trFor("cast", "Name this show")
                                            text: win.service.library.saved
                                            onAccepted: win.service.saveShow(text)
                                        }

                                        DButton {
                                            id: saveButton
                                            anchors.verticalCenter: parent.verticalCenter
                                            buttonHeight: Theme.buttonHeightXS
                                            iconName: "save"
                                            text: I18n.trFor("cast", "Save")
                                            enabled: nameField.text.trim() !== ""
                                            backgroundColor: Theme.secondaryContainer
                                            textColor: Theme.onSecondaryContainer
                                            onClicked: win.service.saveShow(nameField.text)
                                        }
                                    }
                                }

                                SettingsRow {
                                    iconName: win.selectedItem?.missing ? "broken_image" : win.selectedItem?.kind === "video" ? "movie" : win.selectedItem?.kind === "audio" ? "music_note" : "image"
                                    title: win.selectedItem?.name ?? ""
                                    singleLineTitle: true

                                    DActionButton {
                                        anchors.verticalCenter: parent.verticalCenter
                                        iconName: "arrow_back"
                                        enabled: win.selected > 0
                                        tooltipText: I18n.trFor("cast", "Move earlier")
                                        Accessible.name: tooltipText
                                        onClicked: win.service.moveItem(win.selected, win.selected - 1)
                                    }

                                    DActionButton {
                                        anchors.verticalCenter: parent.verticalCenter
                                        iconName: "arrow_forward"
                                        enabled: win.selected < win.items.length - 1
                                        tooltipText: I18n.trFor("cast", "Move later")
                                        Accessible.name: tooltipText
                                        onClicked: win.service.moveItem(win.selected, win.selected + 1)
                                    }

                                    DActionButton {
                                        anchors.verticalCenter: parent.verticalCenter
                                        iconName: "delete"
                                        tooltipText: I18n.trFor("cast", "Remove from the show")
                                        Accessible.name: tooltipText
                                        onClicked: win.service.removeItem(win.selected)
                                    }
                                }
                            }

                            SettingsSectionLabel {
                                text: I18n.trFor("cast", "Photos change")
                            }

                            SettingsGroup {
                                SettingsButtonGroupRow {
                                    model: [I18n.trFor("cast", "Manual")].concat([5, 8, 15].map(n => I18n.trFor("cast", "%1 s").arg(n)))
                                    values: [0, 5, 8, 15]
                                    value: win.service.slideshowInterval
                                    fallbackValue: 8
                                    onValueSelected: value => win.service.setSlideshowInterval(value)
                                }

                                SettingsToggleRow {
                                    iconName: "repeat"
                                    text: I18n.trFor("cast", "Repeat")
                                    checked: win.service.pluginData.slideshowLoop ?? false
                                    onToggled: checked => win.service.setSetting("slideshowLoop", checked)
                                }
                            }

                            SettingsSectionLabel {
                                visible: deviceGroup.visible
                                text: win.deviceName
                            }

                            SettingsGroup {
                                id: deviceGroup
                                visible: win.service.showingLibrary

                                SettingsRow {
                                    visible: win.service.session?.canVolume ?? false
                                    iconName: "volume_up"

                                    body: DSlider {
                                        width: parent.width
                                        minimum: 0
                                        maximum: 100
                                        // A drag assigns value, which would drop a plain binding and stop
                                        // showing changes made on the device.
                                        Binding on value {
                                            value: Math.round((win.service.session?.volume ?? 0) * 100)
                                            restoreMode: Binding.RestoreNone
                                        }
                                        unit: "%"
                                        onSliderDragFinished: finalValue => win.service.setVolume(finalValue / 100)
                                    }
                                }

                                SettingsRow {
                                    readonly property int cached: win.service.sessionQueue.length > 0 ? win.cachedAhead() : 0
                                    readonly property bool waiting: win.service.preparing

                                    iconName: waiting ? "hourglass_top" : "bolt"
                                    iconColor: waiting ? Theme.surfaceText : Theme.success
                                    title: {
                                        if (waiting)
                                            return I18n.trFor("cast", "Preparing the next video");
                                        return cached > 0 ? I18n.trFor("cast", "Next %1 cached").arg(cached) : I18n.trFor("cast", "Caching the next items");
                                    }
                                }

                                SettingsRow {
                                    DButton {
                                        buttonHeight: Theme.buttonHeightXS
                                        iconName: "stop"
                                        text: I18n.trFor("cast", "Stop casting")
                                        backgroundColor: Theme.errorContainer
                                        textColor: Theme.onErrorContainer
                                        onClicked: win.service.stopCast()
                                    }
                                }
                            }
                        }
                    }
                }

                // Transport: previous, the main action, next, and the countdown or seek bar.
                Row {
                    id: transport
                    x: Theme.windowInset
                    y: top.height + Theme.spacingM
                    width: parent.width - Theme.windowInset * 2
                    height: Theme.buttonHeightM
                    spacing: Theme.spacingS

                    readonly property bool active: win.service.showingLibrary
                    readonly property bool manualPhoto: active && win.service.manualPhoto

                    DIconButton {
                        anchors.verticalCenter: parent.verticalCenter
                        iconName: "skip_previous"
                        enabled: transport.active && win.service.sessionIndex > 0
                        Accessible.name: I18n.trFor("cast", "Previous")
                        onClicked: win.service.mediaCommand("previous")
                    }

                    // Play or pause; in manual mode on a photo it moves on to the next one.
                    DIconButton {
                        anchors.verticalCenter: parent.verticalCenter
                        size: "m"
                        widthMode: "wide"
                        variant: "filled"
                        round: false
                        checkable: !transport.manualPhoto
                        checked: transport.active && !transport.manualPhoto && win.service.mediaPlaying
                        iconFilled: false
                        radius: Theme.buttonRadius(width, height, buttonSize, pressed, checked)
                        iconName: transport.manualPhoto ? "skip_next" : checked ? "pause" : "play_arrow"
                        enabled: !!win.device && (!transport.manualPhoto || win.service.sessionIndex < win.service.sessionQueue.length - 1)
                        Accessible.name: transport.manualPhoto ? I18n.trFor("cast", "Next") : checked ? I18n.trFor("cast", "Pause") : I18n.trFor("cast", "Play")
                        onClicked: {
                            if (transport.active)
                                win.service.togglePlay();
                            else
                                win.service.playLibrary(win.device.id, win.selected);
                        }
                    }

                    DIconButton {
                        visible: !transport.manualPhoto
                        anchors.verticalCenter: parent.verticalCenter
                        iconName: "skip_next"
                        enabled: transport.active && win.service.sessionIndex < win.service.sessionQueue.length - 1
                        Accessible.name: I18n.trFor("cast", "Next")
                        onClicked: win.service.mediaCommand("next")
                    }

                    Item {
                        width: Theme.spacingS
                        height: 1
                    }

                    Column {
                        id: countdownColumn
                        anchors.verticalCenter: parent.verticalCenter
                        width: parent.width - x
                        spacing: Theme.spacingXS
                        visible: !win.service.hasTimeline || !transport.active

                        readonly property real countdown: win.service.countdownAt(win.now)

                        StyledRect {
                            visible: transport.active && (win.service.preparing || countdownColumn.countdown >= 0)
                            width: parent.width
                            height: Theme.spacingXS
                            radius: height / 2
                            color: Theme.secondaryContainer

                            StyledRect {
                                height: parent.height
                                radius: parent.radius
                                color: Theme.primary
                                width: {
                                    if (!parent.visible)
                                        return 0;
                                    if (win.service.preparing)
                                        return parent.width * win.service.prepProgress;
                                    const interval = win.service.session?.interval ?? 0;
                                    return interval > 0 ? parent.width * (1 - countdownColumn.countdown / interval) : 0;
                                }
                            }
                        }

                        Item {
                            width: parent.width
                            height: Theme.fontSizeSmall + Theme.spacingXS

                            StyledText {
                                text: {
                                    if (win.service.otherShowPlaying)
                                        return I18n.trFor("cast", "Play replaces the running show");
                                    if (!transport.active)
                                        return I18n.trFor("cast", "Press play to start on %1").arg(win.deviceName);
                                    if (win.service.preparing)
                                        return I18n.trFor("cast", "Preparing %1... %2%").arg(win.service.currentTitle).arg(Math.round(win.service.prepProgress * 100));
                                    if (countdownColumn.countdown >= 0)
                                        return I18n.trFor("cast", "Next photo in %1 s").arg(Math.ceil(countdownColumn.countdown));
                                    if (win.service.slideshowManual)
                                        return I18n.trFor("cast", "Manual");
                                    return I18n.trFor("cast", "Paused");
                                }
                                font.pixelSize: Theme.fontSizeSmall
                                color: Theme.surfaceVariantText
                            }

                            StyledText {
                                anchors.right: parent.right
                                visible: transport.active
                                text: (win.service.sessionIndex + 1) + " / " + win.service.sessionQueue.length
                                font.pixelSize: Theme.fontSizeSmall
                                color: Theme.surfaceVariantText
                            }
                        }
                    }

                    Row {
                        anchors.verticalCenter: parent.verticalCenter
                        width: parent.width - x
                        spacing: Theme.spacingXS
                        visible: win.service.hasTimeline && transport.active

                        DActionButton {
                            anchors.verticalCenter: parent.verticalCenter
                            iconName: "replay_10"
                            Accessible.name: I18n.trFor("cast", "Back 10 seconds")
                            onClicked: win.service.skip(-10)
                        }

                        Column {
                            anchors.verticalCenter: parent.verticalCenter
                            width: parent.width - (Theme.buttonHeightXS + parent.spacing) * (subtitleButton.visible ? 3 : 2)

                            DSlider {
                                width: parent.width
                                minimum: 0
                                maximum: Math.max(1, Math.round(win.service.session?.duration ?? 1))
                                Binding on value {
                                    value: Math.round(win.service.positionAt(win.now))
                                    restoreMode: Binding.RestoreNone
                                }
                                showValue: false
                                onSliderDragFinished: finalValue => win.service.mediaCommand("seek", {
                                        "position": finalValue
                                    })
                            }

                            StyledText {
                                width: parent.width
                                text: win.service.clock(win.service.positionAt(win.now)) + " / " + win.service.clock(win.service.session?.duration ?? 0) + ""
                                elide: Text.ElideRight
                                font.pixelSize: Theme.fontSizeSmall
                                color: Theme.surfaceVariantText
                            }
                        }

                        DActionButton {
                            anchors.verticalCenter: parent.verticalCenter
                            iconName: "forward_10"
                            Accessible.name: I18n.trFor("cast", "Forward 10 seconds")
                            onClicked: win.service.skip(10)
                        }

                        DActionButton {
                            id: subtitleButton
                            anchors.verticalCenter: parent.verticalCenter
                            visible: win.service.session?.subtitles ?? false
                            iconName: (win.service.session?.subtitlesOn ?? false) ? "subtitles" : "subtitles_off"
                            Accessible.name: I18n.trFor("cast", "Subtitles")
                            onClicked: win.service.mediaCommand("subtitles", {
                                "on": !(win.service.session?.subtitlesOn ?? false)
                            })
                        }
                    }
                }

                // Filmstrip: cast badge on what the device shows, a bolt on what's cached,
                // a ring while preparing. Drag a tile to reorder.
                DListView {
                    id: strip
                    x: Theme.windowInset
                    y: transport.y + transport.height + Theme.spacingM
                    width: parent.width - Theme.windowInset * 2
                    height: Math.round(Theme.fontSizeMedium * 5)
                    orientation: ListView.Horizontal
                    spacing: Theme.spacingS
                    clip: true
                    interactive: !stripDragging
                    property bool stripDragging: false
                    model: win.gridMode ? [] : win.items
                    currentIndex: win.selected
                    onCurrentIndexChanged: positionViewAtIndex(currentIndex, ListView.Contain)

                    delegate: Thumb {
                        id: tile
                        required property var modelData
                        required property int index

                        readonly property var entry: win.entryFor(modelData)

                        service: win.service
                        path: modelData.path
                        kind: modelData.kind
                        missing: modelData.missing
                        width: Math.round(height * 1.4)
                        height: strip.height
                        z: tileMouse.drag.active ? 1 : 0
                        radius: Theme.cornerRadiusM
                        border.width: index === win.onDevice || index === win.selected ? Theme.focusRingWidth : 0
                        border.color: index === win.onDevice ? Theme.primary : Theme.surfaceText

                        MouseArea {
                            id: tileMouse
                            anchors.fill: parent
                            cursorShape: drag.active ? Qt.ClosedHandCursor : Qt.PointingHandCursor
                            drag.target: tile
                            drag.axis: Drag.XAxis
                            preventStealing: true
                            onPressed: strip.stripDragging = true
                            onClicked: win.service.librarySelect = tile.index
                            onReleased: {
                                strip.stripDragging = false;
                                const to = Math.round(tile.x / (tile.width + strip.spacing));
                                if (to !== tile.index)
                                    win.service.moveItem(tile.index, to);
                                strip.forceLayout();
                            }
                        }

                        StyledRect {
                            visible: tile.index === win.onDevice || tile.entry?.status === "ready"
                            x: Theme.spacingXS
                            y: Theme.spacingXS
                            width: Theme.iconSizeSmall + Theme.spacingXS
                            height: width
                            radius: width / 2
                            color: Theme.surfaceContainer

                            DIcon {
                                anchors.centerIn: parent
                                name: tile.index === win.onDevice ? "cast" : "bolt"
                                filled: true
                                size: Theme.iconSizeSmall - Theme.spacingXXS
                                color: tile.index === win.onDevice ? Theme.primary : Theme.success
                            }
                        }

                        DIcon {
                            visible: (tile.kind === "video" || tile.kind === "audio") && !tile.missing
                            anchors.right: parent.right
                            anchors.bottom: parent.bottom
                            anchors.margins: Theme.spacingXS
                            name: tile.kind === "audio" ? "music_note" : "movie"
                            size: Theme.iconSizeSmall
                            color: Theme.surfaceText
                        }

                        StyledRect {
                            visible: tile.entry?.status === "failed"
                            anchors.fill: parent
                            radius: parent.radius
                            color: Theme.withAlpha(Theme.errorContainer, Theme.tonalTintAlpha)

                            DIcon {
                                anchors.centerIn: parent
                                name: "error"
                                filled: true
                                size: Theme.iconSize
                                color: Theme.error
                            }
                        }

                        DRingGauge {
                            visible: tile.entry?.status === "preparing"
                            anchors.centerIn: parent
                            width: Theme.iconSize
                            height: width
                            strokeWidth: Theme.spacingXXS * 2
                            value: tile.entry?.progress ?? 0
                            animated: false
                        }
                    }
                }
            }
        }
    }
}
