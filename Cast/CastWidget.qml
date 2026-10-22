pragma ComponentBehavior: Bound

import QtQuick
import Quickshell
import qs.Common
import qs.DCommon.Widgets
import qs.Services
import qs.Modals.FileBrowser
import qs.Modules.ControlCenter
import qs.Modules.ControlCenter.Widgets
import qs.Modules.Plugins

PluginComponent {
    id: root

    property var popoutService: null

    readonly property var service: pluginService?.pluginDaemonInstances[pluginId] ?? null
    readonly property bool casting: service?.casting ?? false
    readonly property bool busy: service?.starting ?? false
    readonly property var session: service?.session ?? null
    readonly property string deviceName: service?.sessionDevice?.name ?? service?.devices.find(d => d.id === service.pendingDevice)?.name ?? ""
    readonly property bool hideWhenIdle: (pluginData.pillOnlyWhileCasting ?? false) && !casting && !busy
    readonly property bool showName: pluginData.showDeviceName ?? true

    // What the pill shows: idle, connecting, mirror, preparing, media or slides.
    readonly property string pillState: {
        if (!casting && !busy)
            return "idle";
        if (busy || session.phase === "starting" || session.phase === "authorizing")
            return "connecting";
        if (session.kind !== "media")
            return "mirror";
        if (service.preparing)
            return "preparing";
        return service.isQueue ? "slides" : "media";
    }
    // In manual mode a photo has no play or pause; next moves on.
    readonly property bool canToggle: (service?.isMedia ?? false) && !service.manualPhoto && (service.isQueue || service.currentKind !== "image")
    readonly property bool ticking: surfaceLive && (pillState === "slides" || pillState === "media") && (service.slideshowRunning || (service.hasTimeline && service.mediaPlaying))
    property double now: Date.now()
    readonly property real progress: {
        if (pillState === "preparing")
            return service.prepProgress;
        if (service?.hasTimeline)
            return service.positionAt(now) / service.session.duration;
        const left = service?.countdownAt(now) ?? -1;
        return left >= 0 ? 1 - left / service.session.interval : 0;
    }

    Timer {
        interval: 1000
        repeat: true
        running: root.ticking
        onTriggered: root.now = Date.now()
    }

    onHideWhenIdleChanged: {
        if (hideWhenIdle)
            setVisibilityOverride(false);
        else
            clearVisibilityOverride();
    }

    Component.onCompleted: {
        if (hideWhenIdle)
            setVisibilityOverride(false);
    }

    function stateText() {
        if (!casting)
            return busy ? I18n.trFor("cast", "Connecting...") : I18n.trFor("cast", "Not casting");
        if (session?.kind === "media")
            return I18n.trFor("cast", "Playing on %1").arg(deviceName);
        return I18n.trFor("cast", "Casting to %1").arg(deviceName);
    }

    Connections {
        target: root.service

        function onPortalMayOpen() {
            root.closePopout();
            PopoutService.closeControlCenter();
        }
    }

    function openSettings() {
        PopoutService.closeControlCenter();
        PopoutService.openSettingsWithTab("plugin:cast");
    }

    component LiveDot: StyledRect {
        id: dot

        property bool pulsing: false

        width: Theme.spacingS
        height: width
        radius: width / 2
        color: Theme.error

        DBlink {
            target: dot
            running: dot.pulsing
        }
    }

    component StopButton: DActionButton {
        required property var service
        required property int size

        buttonSize: size + Theme.spacingXS * 2
        iconSize: size
        iconName: "stop_circle"
        iconFilled: true
        iconColor: Theme.error
        Accessible.name: I18n.trFor("cast", "Stop casting")
        onClicked: service?.stopCast()
    }

    component PillButton: DActionButton {
        required property int size

        buttonSize: size + Theme.spacingXS * 2
        iconSize: size
        iconColor: Theme.widgetIconColor
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

        readonly property string source: kind === "image" ? (service?.fileUrl(path) ?? "") : (service?.thumbs[path] ?? "")

        function fetch() {
            if ((kind === "video" || kind === "audio") && path)
                service?.requestThumbnail(path);
        }

        onPathChanged: fetch()
        onKindChanged: fetch()
        Component.onCompleted: fetch()

        color: Theme.surfaceContainerHighest
        clip: true

        Image {
            id: image
            anchors.fill: parent
            source: thumb.source
            sourceSize.width: 256
            sourceSize.height: 256
            asynchronous: true
            fillMode: Image.PreserveAspectCrop
            visible: status === Image.Ready
        }

        DIcon {
            anchors.centerIn: parent
            visible: image.status !== Image.Ready
            name: thumb.kind === "image" ? "image" : thumb.kind === "audio" ? "music_note" : thumb.path ? "movie" : "smart_display"
            size: Math.min(Theme.iconSize, thumb.height * 0.7)
            color: Theme.surfaceVariantText
        }
    }

    horizontalBarPill: Component {
        Item {
            id: pill

            readonly property string mode: root.pillState

            implicitWidth: row.implicitWidth
            implicitHeight: row.implicitHeight

            Behavior on implicitWidth {
                NumberAnimation {
                    duration: Theme.mediumDuration
                    easing.type: Easing.BezierSpline
                    easing.bezierCurve: Theme.expressiveCurves.emphasizedDecel
                }
            }

            Row {
                id: row
                spacing: Theme.spacingXS

                DIcon {
                    visible: pill.mode === "idle"
                    anchors.verticalCenter: parent.verticalCenter
                    name: "cast"
                    size: root.iconSize
                    color: Theme.widgetIconColor
                }

                DLoadingIndicator {
                    visible: pill.mode === "connecting"
                    anchors.verticalCenter: parent.verticalCenter
                    size: root.iconSize
                }

                Item {
                    visible: pill.mode === "mirror"
                    anchors.verticalCenter: parent.verticalCenter
                    width: root.iconSize
                    height: root.iconSize

                    LiveDot {
                        anchors.centerIn: parent
                        pulsing: pill.mode === "mirror" && root.surfaceLive
                    }
                }

                DRingGauge {
                    visible: pill.mode === "preparing"
                    anchors.verticalCenter: parent.verticalCenter
                    width: root.iconSize
                    height: width
                    strokeWidth: Theme.spacingXXS
                    value: root.progress
                    animated: false
                }

                Thumb {
                    visible: pill.mode === "media" || pill.mode === "slides"
                    anchors.verticalCenter: parent.verticalCenter
                    service: root.service
                    path: root.service?.currentItem?.path ?? ""
                    kind: root.service?.currentKind ?? ""
                    width: root.iconSize
                    height: width
                    radius: Theme.cornerRadiusS
                }

                StyledText {
                    visible: text !== ""
                    anchors.verticalCenter: parent.verticalCenter
                    width: Math.min(implicitWidth, root.textSize * 12)
                    elide: Text.ElideRight
                    text: {
                        switch (pill.mode) {
                        case "connecting":
                        case "mirror":
                            return root.showName ? root.deviceName : "";
                        case "preparing":
                        case "media":
                            return root.showName ? root.service.currentTitle : "";
                        case "slides":
                            return (root.service.sessionIndex + 1) + " / " + root.service.sessionQueue.length;
                        }
                        return "";
                    }
                    font.pixelSize: root.textSize
                    color: pill.mode === "preparing" ? Theme.surfaceVariantText : Theme.widgetTextColor
                }

                PillButton {
                    visible: (pill.mode === "media" || pill.mode === "slides") && root.canToggle
                    anchors.verticalCenter: parent.verticalCenter
                    size: root.iconSize
                    iconName: root.service?.mediaPlaying ? "pause" : "play_arrow"
                    Accessible.name: root.service?.mediaPlaying ? I18n.trFor("cast", "Pause") : I18n.trFor("cast", "Play")
                    onClicked: root.service.togglePlay()
                }

                PillButton {
                    visible: pill.mode === "slides"
                    anchors.verticalCenter: parent.verticalCenter
                    size: root.iconSize
                    iconName: "skip_next"
                    Accessible.name: I18n.trFor("cast", "Next")
                    onClicked: root.service.mediaCommand("next")
                }

                StopButton {
                    visible: pill.mode !== "idle"
                    service: root.service
                    size: root.iconSize
                    anchors.verticalCenter: parent.verticalCenter
                }
            }

            // Playback progress, or the slideshow running down its interval.
            StyledRect {
                visible: pill.mode === "media" || pill.mode === "slides"
                anchors.top: parent.bottom
                anchors.topMargin: Theme.spacingXXS
                width: parent.width
                height: Theme.spacingXXS
                radius: height / 2
                color: Theme.outlineVariant

                StyledRect {
                    height: parent.height
                    radius: parent.radius
                    width: parent.width * Math.max(0, Math.min(1, root.progress))
                    color: Theme.primary
                }
            }
        }
    }

    verticalBarPill: Component {
        Column {
            id: vpill

            readonly property string mode: root.pillState

            spacing: Theme.spacingXS

            DIcon {
                visible: vpill.mode === "idle"
                anchors.horizontalCenter: parent.horizontalCenter
                name: "cast"
                size: root.iconSize
                color: Theme.widgetIconColor
            }

            DLoadingIndicator {
                visible: vpill.mode === "connecting"
                anchors.horizontalCenter: parent.horizontalCenter
                size: root.iconSize
            }

            LiveDot {
                visible: vpill.mode === "mirror"
                pulsing: visible && root.surfaceLive
                anchors.horizontalCenter: parent.horizontalCenter
            }

            StyledText {
                visible: vpill.mode === "slides"
                anchors.horizontalCenter: parent.horizontalCenter
                horizontalAlignment: Text.AlignHCenter
                text: visible ? (root.service.sessionIndex + 1) + "\n" + root.service.sessionQueue.length : ""
                font.pixelSize: Theme.fontSizeSmall
                font.weight: Theme.fontWeightMedium
                color: Theme.widgetTextColor
            }

            // A ring around play or pause shows progress; while preparing it's empty inside.
            DRingGauge {
                visible: vpill.mode === "preparing" || vpill.mode === "media" || vpill.mode === "slides"
                anchors.horizontalCenter: parent.horizontalCenter
                width: root.iconSize + Theme.spacingXS * 2
                height: width
                strokeWidth: Theme.spacingXXS
                value: root.progress
                animated: false

                PillButton {
                    anchors.centerIn: parent
                    visible: vpill.mode !== "preparing" && root.canToggle
                    size: root.iconSize - Theme.spacingXS * 2
                    iconName: root.service?.mediaPlaying ? "pause" : "play_arrow"
                    Accessible.name: root.service?.mediaPlaying ? I18n.trFor("cast", "Pause") : I18n.trFor("cast", "Play")
                    onClicked: root.service.togglePlay()
                }
            }

            StopButton {
                visible: vpill.mode !== "idle"
                service: root.service
                size: root.iconSize
                anchors.horizontalCenter: parent.horizontalCenter
            }
        }
    }

    popoutWidth: 400

    popoutContent: Component {
        PopoutComponent {
            id: popout

            headerText: I18n.trFor("cast", "Cast")
            detailsText: root.stateText()

            headerActions: Component {
                DActionButton {
                    buttonSize: CcMetrics.headerActionSize
                    iconSize: CcMetrics.headerActionIconSize
                    iconName: "settings"
                    iconColor: Theme.surfaceText
                    tooltipText: I18n.trFor("cast", "Cast settings")
                    Accessible.name: tooltipText
                    onClicked: {
                        if (popout.closePopout)
                            popout.closePopout();
                        root.openSettings();
                    }
                }
            }

            DevicePanel {
                width: parent.width
                service: root.service
                parentPopout: popout.parentPopout
                shown: popout.parentPopout?.shouldBeVisible ?? true
                onOpenSettings: {
                    if (popout.closePopout)
                        popout.closePopout();
                    root.openSettings();
                }
            }
        }
    }

    ccWidgetIcon: casting ? "cast_connected" : "cast"
    ccWidgetPrimaryText: I18n.trFor("cast", "Cast")
    ccWidgetSecondaryText: {
        if (casting)
            return deviceName;
        if (busy)
            return I18n.trFor("cast", "Connecting...");
        const favorite = pluginData.favoriteName ?? "";
        return favorite ? I18n.trFor("cast", "Off • %1").arg(favorite) : I18n.trFor("cast", "Off");
    }
    ccWidgetIsActive: casting || busy
    ccDetailHeight: Theme.listItemTwoLineHeight * 7

    onCcWidgetToggled: {
        if (service?.toggleFavorite())
            return;
        ToastService?.showInfo(I18n.trFor("cast", "Pin a device in the panel first"));
    }

    ccDetailContent: Component {
        DFlickable {
            contentHeight: detailPanel.implicitHeight
            clip: true

            // The Control Center doesn't show a plugin's header actions, so settings is the last row.
            DevicePanel {
                id: detailPanel
                width: parent.width
                service: root.service
                showSettingsRow: true
                onOpenSettings: root.openSettings()
            }
        }
    }

    // Choices for the panel's options; Settings.qml keeps a copy, since the
    // shell recompiles only a plugin's own entry files on reload.
    component CastOptions: QtObject {
        readonly property var audioTargets: [
            {
                "label": I18n.trFor("cast", "This device"),
                "value": "local"
            },
            {
                "label": I18n.trFor("cast", "Cast device"),
                "value": "tv"
            },
            {
                "label": I18n.trFor("cast", "Both"),
                "value": "both"
            }
        ]

        readonly property var qualities: [
            {
                "label": I18n.trFor("cast", "Smooth (1080p, 60 fps)"),
                "value": "smooth"
            },
            {
                "label": I18n.trFor("cast", "Balanced (1080p, 30 fps)"),
                "value": "balanced"
            },
            {
                "label": I18n.trFor("cast", "Low bandwidth (720p, 30 fps)"),
                "value": "low"
            },
            {
                "label": I18n.trFor("cast", "Custom"),
                "value": "custom"
            }
        ]

        function labelOf(list, value) {
            return (list.find(o => o.value === value) ?? list[0]).label;
        }

        function valueOf(list, label) {
            return (list.find(o => o.label === label) ?? list[0]).value;
        }
    }

    // Device list, now playing and cast controls, shared by the popout and the
    // Control Center detail panel.
    component DevicePanel: Column {
        id: panel

        required property var service
        property bool shown: true
        property var parentPopout: null
        property bool showSettingsRow: false
        // A dropdown's menu is its own window. Registering it with the host's
        // tracker keeps the popout or Control Center open while it's clicked.
        property var transientSurfaceTracker: parentPopout ? parentPopout.transientSurfaceTracker : (PopoutService.controlCenterPopout?.transientSurfaceTracker ?? null)

        signal openSettings

        readonly property string demandKey: "panel" + Math.random()
        readonly property bool helperMissing: service && service.helperResolved && !service.helperPath
        readonly property bool needsSetup: helperMissing
        readonly property var session: service?.session ?? null
        readonly property bool isMedia: service?.isMedia ?? false
        readonly property bool connecting: !!session && (session.phase === "starting" || session.phase === "authorizing" || session.phase === "pairing")
        readonly property bool pairing: session?.phase === "pairing"
        readonly property var devices: service?.devices ?? []
        readonly property bool ready: !!service && !needsSetup

        // Where "Send to" plays while nothing casts; the running cast's device otherwise.
        property string sendToId: ""
        // Files and links go over Chromecast, so "Send to" only offers devices that speak it.
        readonly property var mediaDevices: service?.mediaDevices ?? []
        readonly property var sendTarget: session ? (service.canMedia(service.sessionDevice) ? service.sessionDevice : null) : (mediaDevices.find(d => d.id === sendToId) ?? service?.defaultMediaDevice ?? null)
        readonly property var others: !service ? [] : devices.filter(d => d.id !== session?.device && !(session?.kind === "mirror" && /speaker/.test(service.deviceIcon(d))) && (session?.kind === "mirror" || service.canMedia(d)))

        property double now: Date.now()
        Timer {
            interval: 1000
            repeat: true
            running: panel.shown && (panel.service?.slideshowRunning || (panel.service?.hasTimeline && panel.service?.mediaPlaying)) === true
            onTriggered: panel.now = Date.now()
        }

        // Tips show after searching a while without finding anything.
        property bool searchedLong: false
        Timer {
            interval: 10000
            running: panel.shown && panel.ready && panel.devices.length === 0
            onTriggered: panel.searchedLong = true
        }

        property string pickDevice: ""
        property string link: ""
        readonly property bool hasLink: link.trim() !== ""

        function playLink() {
            if (!hasLink)
                return;
            if (!sendTarget) {
                ToastService?.showInfo(I18n.trFor("cast", "Pick a cast device first"));
                return;
            }
            service.playLink(sendTarget.id, link.trim());
            link = "";
        }

        function pickFile() {
            if (!sendTarget) {
                ToastService?.showInfo(I18n.trFor("cast", "Pick a cast device first"));
                return;
            }
            pickDevice = sendTarget.id;
            fileBrowserLoader.active = true;
            fileBrowserLoader.item?.open();
        }

        function openPlayer(path) {
            service.openPlayer(path);
            if (parentPopout?.close)
                parentPopout.close();
            PopoutService.closeControlCenter();
        }

        function phaseText() {
            const name = service.sessionDevice?.name ?? "";
            switch (session?.phase) {
            case "authorizing":
                return I18n.trFor("cast", "Accept the cast on %1").arg(name);
            case "starting":
                return I18n.trFor("cast", "Starting...");
            case "pairing":
                return I18n.trFor("cast", "Pairing with AirPlay");
            case "stopping":
                return I18n.trFor("cast", "Stopping...");
            }
            return I18n.trFor("cast", "Mirroring");
        }

        function mediaSubtitle() {
            const name = service.sessionDevice?.name ?? "";
            if (service.currentEntry?.status === "failed")
                return service.itemErrorText(service.currentEntry);
            if (service.preparing)
                return I18n.trFor("cast", "Preparing... %1%").arg(Math.round(service.prepProgress * 100));
            if (service.isQueue)
                return (service.currentArtist ? service.currentArtist + " · " : "") + name + " · " + I18n.trFor("cast", "%1 of %2").arg(service.sessionIndex + 1).arg(service.sessionQueue.length);
            if (service.currentArtist)
                return service.currentArtist + " · " + name;
            if (service.isLink)
                return name + (/youtu/.test(service.currentItem.url) ? " · YouTube" : "");
            if (session?.playerState === "BUFFERING")
                return name + " · " + I18n.trFor("cast", "Loading...");
            return name;
        }

        Loader {
            id: fileBrowserLoader
            active: false
            sourceComponent: FileBrowserSurfaceModal {
                browserTitle: I18n.trFor("cast", "Play on the cast device")
                bucket: "cast"
                filters: panel.service ? panel.service.pickerFilters.concat(["*"]) : ["*"]
                parentPopout: panel.parentPopout
                onAccepted: paths => panel.service.playFile(panel.pickDevice, paths[0])
            }
        }

        spacing: CcMetrics.detailContentGap

        onShownChanged: service?.setDemand(demandKey, shown)
        Component.onCompleted: service?.setDemand(demandKey, shown)
        Component.onDestruction: service?.setDemand(demandKey, false)

        CcEmptyState {
            width: parent.width
            visible: !panel.service
            iconName: "cast"
            title: I18n.trFor("cast", "Cast is starting")
        }

        // Setup required
        CcEmptyState {
            width: parent.width
            visible: panel.helperMissing
            iconName: "build"
            title: I18n.trFor("cast", "Cast helper not installed")
            subtitle: panel.service?.onArch ? I18n.trFor("cast", "Install it from the AUR, which also installs the packages it needs") : I18n.trFor("cast", "Download it with this command, or build it from source")

            CommandRow {
                width: parent.width
                command: panel.service?.onArch ? "yay -S cast-helper" : ""
            }

            CommandRow {
                width: parent.width
                label: panel.service?.onArch ? I18n.trFor("cast", "Or download") : ""
                command: panel.service?.helperDownloadCommand ?? ""
            }

            Row {
                anchors.horizontalCenter: parent.horizontalCenter
                spacing: Theme.spacingS

                DButton {
                    buttonHeight: Theme.buttonHeightXS
                    iconName: "open_in_new"
                    text: I18n.trFor("cast", "Installation guide")
                    backgroundColor: Theme.secondaryContainer
                    textColor: Theme.onSecondaryContainer
                    onClicked: Qt.openUrlExternally(panel.service.installGuide)
                }

                DButton {
                    buttonHeight: Theme.buttonHeightXS
                    iconName: "refresh"
                    text: I18n.trFor("cast", "Check again")
                    onClicked: panel.service.findHelper()
                }
            }
        }

        CcGroup {
            visible: !panel.helperMissing && (panel.service?.missing.length ?? 0) > 0

            CcEmptyState {
                iconName: "build"
                title: I18n.trFor("cast", "A few things are missing")
            }
        }

        CcSectionLabel {
            visible: missingGroup.visible
            text: I18n.trFor("cast", "Missing")
        }

        CcGroup {
            id: missingGroup
            visible: !panel.helperMissing && (panel.service?.missing.length ?? 0) > 0

            Repeater {
                model: panel.service?.missing ?? []

                CcListRow {
                    id: missingRow
                    required property var modelData
                    readonly property var dep: panel.service.deps.find(d => d.id === modelData) ?? null

                    iconName: "error"
                    iconColor: Theme.error
                    title: panel.service.packagesFor([modelData])[0] ?? modelData
                    subtitle: dep ? I18n.trFor("cast", "Required for: %1").arg(panel.service.featuresText(dep.features)) : ""

                    body: PackageHints {
                        width: parent.width
                        service: panel.service
                        ids: [missingRow.modelData]
                    }
                }
            }

            CcListRow {
                body: CommandRow {
                    width: parent.width
                    command: panel.service?.installCommand(panel.service.missing) ?? ""
                }
            }

            CcListRow {
                DButton {
                    anchors.verticalCenter: parent.verticalCenter
                    buttonHeight: Theme.buttonHeightXS
                    iconName: "refresh"
                    text: I18n.trFor("cast", "Check again")
                    onClicked: panel.service.recheck()
                }
            }
        }

        // Now casting: mirroring or connecting
        CcGroup {
            visible: !!panel.session && (!panel.isMedia || panel.connecting)

            CcListRow {
                active: true
                title: panel.service?.sessionDevice?.name ?? ""
                subtitle: panel.session ? panel.phaseText() : ""

                leading: Item {
                    width: Theme.iconSize
                    height: Theme.iconSize

                    DLoadingIndicator {
                        anchors.centerIn: parent
                        visible: panel.connecting
                        size: Theme.iconSize
                        color: Theme.onPrimaryContainer
                    }

                    LiveDot {
                        anchors.centerIn: parent
                        visible: !panel.connecting
                        pulsing: visible && panel.shown
                    }
                }

                DButton {
                    anchors.verticalCenter: parent.verticalCenter
                    buttonHeight: Theme.buttonHeightXS
                    iconName: panel.connecting ? "close" : "stop"
                    text: panel.connecting ? I18n.trFor("cast", "Cancel") : I18n.trFor("cast", "Stop")
                    backgroundColor: panel.connecting ? Theme.secondaryContainer : Theme.error
                    textColor: panel.connecting ? Theme.onSecondaryContainer : Theme.onError
                    onClicked: panel.service.stopCast()
                }
            }

            // AirPlay pairing: the code shown on the TV, or the receiver's password.
            CcListRow {
                id: pairRow

                readonly property bool password: panel.session?.credential === "password"

                visible: panel.pairing
                iconName: "password"
                title: password ? I18n.trFor("cast", "Enter the AirPlay password") : I18n.trFor("cast", "Enter the code shown on %1").arg(panel.service?.sessionDevice?.name ?? "")

                body: Row {
                    width: parent.width
                    spacing: Theme.spacingS

                    DTextField {
                        id: codeField
                        width: parent.width - pairButton.width - parent.spacing
                        placeholderText: pairRow.password ? I18n.trFor("cast", "Password") : I18n.trFor("cast", "Code")
                        echoMode: pairRow.password ? TextInput.Password : TextInput.Normal
                        validator: RegularExpressionValidator {
                            regularExpression: pairRow.password ? /.*/ : /\d{0,8}/
                        }
                        onAccepted: pairButton.clicked()
                    }

                    DButton {
                        id: pairButton
                        anchors.verticalCenter: parent.verticalCenter
                        buttonHeight: Theme.buttonHeightXS
                        text: I18n.trFor("cast", "Pair")
                        enabled: codeField.text !== ""
                        onClicked: {
                            panel.service.pair(codeField.text);
                            codeField.text = "";
                        }
                    }
                }
            }

            CcListRow {
                visible: !panel.connecting && (panel.session?.canVolume ?? false)
                iconName: "volume_up"

                body: DSlider {
                    width: parent.width
                    minimum: 0
                    maximum: 100
                    // A drag assigns value, which would drop a plain binding and stop
                    // showing changes made on the device.
                    Binding on value {
                        value: Math.round((panel.session?.volume ?? 0) * 100)
                        restoreMode: Binding.RestoreNone
                    }
                    unit: "%"
                    onSliderDragFinished: finalValue => panel.service.setVolume(finalValue / 100)
                }
            }

            CcListRow {
                id: soundRow

                readonly property var warning: panel.service?.soundWarning ?? null

                visible: !!warning && !panel.connecting
                iconName: "info"
                iconColor: Theme.surfaceVariantText
                title: warning?.code === "airplaySound" ? I18n.trFor("cast", "Sound plays on this computer") : I18n.trFor("cast", "Sound also plays on this computer")
                subtitle: {
                    if (warning?.code === "airplaySound")
                        return I18n.trFor("cast", "AirPlay would set the device to full volume");
                    return warning?.missing?.length ? I18n.trFor("cast", "Install %1 to play it only on the cast device").arg(panel.service.packagesFor(warning.missing).join(", ")) : "";
                }

                body: Column {
                    width: parent.width
                    spacing: Theme.spacingS

                    CommandRow {
                        width: parent.width
                        command: soundRow.warning?.missing?.length ? panel.service.installCommand(soundRow.warning.missing) : ""
                    }

                    PackageHints {
                        width: parent.width
                        service: panel.service
                        ids: soundRow.warning?.missing ?? []
                    }
                }
            }
        }

        // Now playing: a file, a link or the player's queue
        CcGroup {
            visible: panel.isMedia && !panel.connecting

            CcListRow {
                body: Column {
                    width: parent.width
                    spacing: Theme.spacingM

                    Row {
                        width: parent.width
                        spacing: Theme.spacingM

                        Thumb {
                            service: panel.service
                            path: panel.service?.currentItem?.path ?? ""
                            kind: panel.service?.currentKind ?? ""
                            width: Theme.listItemTwoLineHeight * 4 / 3
                            height: width * 9 / 16
                            radius: Theme.cornerRadiusM
                        }

                        Column {
                            anchors.verticalCenter: parent.verticalCenter
                            width: parent.width - x
                            spacing: Theme.spacingXXS

                            StyledText {
                                width: parent.width
                                text: panel.service?.currentTitle ?? ""
                                elide: Text.ElideMiddle
                                font.pixelSize: Theme.fontSizeMedium
                                font.weight: Theme.fontWeightMedium
                                color: Theme.surfaceText
                            }

                            StyledText {
                                width: parent.width
                                text: panel.isMedia ? panel.mediaSubtitle() : ""
                                elide: Text.ElideRight
                                font.pixelSize: Theme.fontSizeSmall
                                color: Theme.surfaceVariantText
                            }
                        }
                    }

                    DSlider {
                        visible: (panel.service?.hasTimeline ?? false) && !panel.service.preparing
                        width: parent.width
                        minimum: 0
                        maximum: Math.max(1, Math.round(panel.session?.duration ?? 1))
                        Binding on value {
                            value: Math.round(panel.service?.positionAt(panel.now) ?? 0)
                            restoreMode: Binding.RestoreNone
                        }
                        showValue: false
                        onSliderDragFinished: finalValue => panel.service.mediaCommand("seek", {
                                "position": finalValue
                            })
                    }

                    // Preparing, or the slideshow running down its interval.
                    StyledRect {
                        id: bar
                        visible: panel.isMedia && (panel.service.preparing || panel.service.isQueue && !panel.service.hasTimeline && !panel.service.slideshowManual)
                        readonly property real countdown: panel.service?.countdownAt(panel.now) ?? -1
                        width: parent.width
                        height: Theme.spacingXS
                        radius: height / 2
                        color: Theme.secondaryContainer

                        StyledRect {
                            height: parent.height
                            radius: parent.radius
                            color: Theme.primary
                            width: {
                                if (!bar.visible)
                                    return 0;
                                if (panel.service.preparing)
                                    return parent.width * panel.service.prepProgress;
                                return bar.countdown >= 0 ? parent.width * (1 - bar.countdown / panel.session.interval) : 0;
                            }
                        }
                    }

                    Item {
                        visible: !(panel.service?.preparing ?? true) && (panel.service.hasTimeline || panel.service.isQueue)
                        width: parent.width
                        height: Theme.fontSizeSmall + Theme.spacingXS

                        StyledText {
                            text: {
                                if (!parent.visible)
                                    return "";
                                if (panel.service.hasTimeline)
                                    return panel.service.clock(panel.service.positionAt(panel.now));
                                if (bar.countdown >= 0)
                                    return I18n.trFor("cast", "Next in %1 s").arg(Math.ceil(bar.countdown));
                                return panel.service.slideshowManual ? I18n.trFor("cast", "Manual") : I18n.trFor("cast", "Paused");
                            }
                            font.pixelSize: Theme.fontSizeSmall
                            color: Theme.surfaceVariantText
                        }

                        StyledText {
                            anchors.right: parent.right
                            text: {
                                if (!parent.visible)
                                    return "";
                                if (panel.service.hasTimeline)
                                    return panel.service.clock(panel.session.duration);
                                return (panel.service.sessionIndex + 1) + " / " + panel.service.sessionQueue.length;
                            }
                            font.pixelSize: Theme.fontSizeSmall
                            color: Theme.surfaceVariantText
                        }
                    }

                    // Transport: the main action is the large filled button.
                    Row {
                        readonly property bool queue: panel.service?.isQueue ?? false
                        visible: !(panel.service?.preparing ?? true) && (queue || panel.service.currentKind !== "image")
                        anchors.horizontalCenter: parent.horizontalCenter
                        spacing: Theme.spacingM

                        DIconButton {
                            anchors.verticalCenter: parent.verticalCenter
                            visible: !!parent.queue || (panel.service?.hasTimeline ?? false)
                            iconName: parent.queue ? "skip_previous" : "replay_10"
                            enabled: !parent.queue || (panel.service?.sessionIndex ?? 0) > 0
                            Accessible.name: parent.queue ? I18n.trFor("cast", "Previous") : I18n.trFor("cast", "Back 10 seconds")
                            onClicked: parent.queue ? panel.service.mediaCommand("previous") : panel.service.skip(-10)
                        }

                        DIconButton {
                            anchors.verticalCenter: parent.verticalCenter
                            size: "m"
                            widthMode: "wide"
                            variant: "filled"
                            round: false
                            readonly property bool manualPhoto: panel.service?.manualPhoto ?? false

                            checkable: !manualPhoto
                            checked: !manualPhoto && (panel.service?.mediaPlaying ?? false)
                            iconFilled: false
                            radius: Theme.buttonRadius(width, height, buttonSize, pressed, checked)
                            iconName: manualPhoto ? "skip_next" : checked ? "pause" : "play_arrow"
                            enabled: !manualPhoto || panel.service.sessionIndex < panel.service.sessionQueue.length - 1
                            Accessible.name: manualPhoto ? I18n.trFor("cast", "Next") : checked ? I18n.trFor("cast", "Pause") : I18n.trFor("cast", "Play")
                            onClicked: panel.service.togglePlay()
                        }

                        DIconButton {
                            anchors.verticalCenter: parent.verticalCenter
                            visible: (parent.queue && !(panel.service?.manualPhoto ?? false)) || (panel.service?.hasTimeline ?? false)
                            iconName: parent.queue ? "skip_next" : "forward_10"
                            enabled: !parent.queue || panel.service.sessionIndex < panel.service.sessionQueue.length - 1
                            Accessible.name: parent.queue ? I18n.trFor("cast", "Next") : I18n.trFor("cast", "Forward 10 seconds")
                            onClicked: parent.queue ? panel.service.mediaCommand("next") : panel.service.skip(10)
                        }
                    }
                }
            }

            CcListRow {
                visible: (panel.session?.canVolume ?? false) && !(panel.service?.preparing ?? true)
                iconName: "volume_up"

                body: DSlider {
                    width: parent.width
                    minimum: 0
                    maximum: 100
                    // A drag assigns value, which would drop a plain binding and stop
                    // showing changes made on the device.
                    Binding on value {
                        value: Math.round((panel.session?.volume ?? 0) * 100)
                        restoreMode: Binding.RestoreNone
                    }
                    unit: "%"
                    onSliderDragFinished: finalValue => panel.service.setVolume(finalValue / 100)
                }
            }

            CcToggleRow {
                visible: panel.session?.subtitles ?? false
                iconName: "subtitles"
                text: I18n.trFor("cast", "Subtitles")
                checked: panel.session?.subtitlesOn ?? false
                onToggled: checked => panel.service.mediaCommand("subtitles", {
                        "on": checked
                    })
            }

            CcListRow {
                DButton {
                    anchors.verticalCenter: parent.verticalCenter
                    visible: !(panel.service?.isLink ?? true) && !(panel.service?.preparing ?? true)
                    buttonHeight: Theme.buttonHeightXS
                    iconName: "view_carousel"
                    text: panel.service?.queueFromLibrary ? I18n.trFor("cast", "Open the player") : I18n.trFor("cast", "Open in player")
                    backgroundColor: Theme.secondaryContainer
                    textColor: Theme.onSecondaryContainer
                    onClicked: panel.openPlayer(panel.service.queueFromLibrary ? "" : (panel.service.currentItem?.path ?? ""))
                }

                DButton {
                    anchors.verticalCenter: parent.verticalCenter
                    buttonHeight: Theme.buttonHeightXS
                    iconName: "stop"
                    text: I18n.trFor("cast", "Stop")
                    backgroundColor: Theme.errorContainer
                    textColor: Theme.onErrorContainer
                    onClicked: panel.service.stopCast()
                }
            }
        }

        CcEmptyState {
            width: parent.width
            visible: !!panel.service?.lastError && !panel.session
            iconName: "error"
            iconColor: Theme.error
            title: panel.service?.errorTitle(panel.service.lastError) ?? ""
            subtitle: panel.service?.errorHint(panel.service?.lastError) ?? ""

            CommandRow {
                width: parent.width
                command: panel.service?.lastError?.code === "missingDependency" ? panel.service.installCommand(panel.service.lastError.missing) : ""
            }

            PackageHints {
                width: parent.width
                service: panel.service
                ids: panel.service?.lastError?.code === "missingDependency" ? (panel.service.lastError.missing ?? []) : []
            }
        }

        // Devices, when nothing casts
        CcGroup {
            visible: panel.ready && !panel.session && deviceRepeater.count > 0

            Repeater {
                id: deviceRepeater
                model: panel.session ? [] : panel.devices

                CcListRow {
                    id: deviceRow

                    required property var modelData

                    readonly property bool isStarting: panel.service.starting && panel.service.pendingDevice === modelData.id

                    iconName: panel.service.deviceIcon(modelData)
                    title: modelData.name
                    subtitle: isStarting ? I18n.trFor("cast", "Connecting...") : (modelData.model ?? "")
                    clickable: !panel.service.starting
                    onClicked: panel.service.startCast(modelData.id)

                    // Which protocol mirrors, only for devices that speak both.
                    DButtonGroup {
                        anchors.verticalCenter: parent.verticalCenter
                        visible: panel.service?.hasBothProtocols(deviceRow.modelData) ?? false
                        size: "small"
                        iconOnly: true
                        checkEnabled: false
                        model: [
                            {
                                "icon": "cast",
                                "tooltip": I18n.trFor("cast", "Mirror over Chromecast")
                            },
                            {
                                "icon": "airplay",
                                "tooltip": I18n.trFor("cast", "Mirror over AirPlay")
                            }
                        ]
                        currentIndex: panel.service.mirrorProtocol(deviceRow.modelData) === "airplay" ? 1 : 0
                        onSelectionChanged: (index, selected) => {
                            if (selected)
                                panel.service.setMirrorProtocol(deviceRow.modelData.id, index === 1 ? "airplay" : "chromecast");
                        }
                    }

                    CcPinChip {
                        anchors.verticalCenter: parent.verticalCenter
                        pinned: panel.service?.favorite === deviceRow.modelData.id
                        // Next to the protocol choice the label doesn't fit.
                        iconOnly: !pinned || (panel.service?.hasBothProtocols(deviceRow.modelData) ?? false)
                        onToggled: panel.service.setFavorite(deviceRow.modelData)
                    }
                }
            }
        }

        // Send to: other devices take over a running cast; the rows below play on the target.
        CcSectionLabel {
            visible: sendGroup.visible
            text: panel.session ? I18n.trFor("cast", "Send to...") : I18n.trFor("cast", "Send to %1").arg(panel.sendTarget?.name ?? "")

            actions: DDropdown {
                transientSurfaceTracker: panel.transientSurfaceTracker
                visible: !panel.session && panel.mediaDevices.length > 1
                compactMode: true
                dropdownWidth: CcMetrics.headerDropdownWidth
                alignPopupRight: true
                options: panel.mediaDevices.map(d => d.name)
                currentValue: panel.sendTarget?.name ?? ""
                onValueChanged: value => panel.sendToId = panel.mediaDevices.find(d => d.name === value)?.id ?? ""
            }
        }

        CcGroup {
            visible: panel.ready && !!panel.session && !panel.connecting && panel.others.length > 0

            Repeater {
                model: panel.session ? panel.others : []

                CcListRow {
                    id: moveRow
                    required property var modelData

                    iconName: panel.service.deviceIcon(modelData)
                    title: modelData.name
                    subtitle: {
                        if (panel.service.isQueue)
                            return I18n.trFor("cast", "Move the slideshow here");
                        if (panel.isMedia)
                            return I18n.trFor("cast", "Move playback here");
                        return I18n.trFor("cast", "Move the cast here");
                    }
                    clickable: true
                    onClicked: panel.service.moveTo(modelData.id)

                    CcPinChip {
                        anchors.verticalCenter: parent.verticalCenter
                        pinned: panel.service.favorite === moveRow.modelData.id
                        iconOnly: !pinned
                        onToggled: panel.service.setFavorite(moveRow.modelData)
                    }
                }
            }
        }

        CcGroup {
            id: sendGroup
            visible: panel.ready && !panel.connecting && !!panel.sendTarget

            CcListRow {
                visible: !panel.service?.isQueue
                body: Row {
                    width: parent.width
                    spacing: Theme.spacingS

                    DTextField {
                        width: parent.width - linkButton.width - parent.spacing
                        leftIconName: "link"
                        showClearButton: true
                        placeholderText: I18n.trFor("cast", "Paste a link")
                        text: panel.link
                        onTextEdited: panel.link = text
                        onAccepted: panel.playLink()
                    }

                    DIconButton {
                        id: linkButton
                        anchors.verticalCenter: parent.verticalCenter
                        variant: "tonal"
                        iconName: "play_arrow"
                        enabled: panel.hasLink
                        Accessible.name: I18n.trFor("cast", "Play the link")
                        onClicked: panel.playLink()
                    }
                }
            }

            CcListRow {
                iconName: "play_circle"
                title: I18n.trFor("cast", "Play a file...")
                clickable: true
                onClicked: panel.pickFile()
            }

            CcListRow {
                visible: !panel.isMedia
                iconName: "playlist_play"
                title: I18n.trFor("cast", "Open the player")
                clickable: true
                onClicked: panel.openPlayer("")

                DIcon {
                    anchors.verticalCenter: parent.verticalCenter
                    name: "open_in_new"
                    size: Theme.iconSize
                    color: Theme.surfaceVariantText
                }
            }
        }

        // No devices yet
        CcGroup {
            visible: panel.ready && panel.devices.length === 0 && !panel.session

            Item {
                width: parent.width
                implicitHeight: searching.implicitHeight + Theme.spacingL * 2
                height: implicitHeight

                Column {
                    id: searching
                    anchors.centerIn: parent
                    width: parent.width - Theme.spacingL * 2
                    spacing: Theme.spacingS

                    DLoadingIndicator {
                        anchors.horizontalCenter: parent.horizontalCenter
                        size: CcMetrics.emptyStateIconSize
                        running: visible && panel.shown
                    }

                    StyledText {
                        width: parent.width
                        text: I18n.trFor("cast", "Looking for cast devices...")
                        font.pixelSize: Theme.fontSizeMedium
                        font.weight: Theme.fontWeightMedium
                        color: Theme.surfaceText
                        horizontalAlignment: Text.AlignHCenter
                        wrapMode: Text.WordWrap
                    }
                }
            }
        }

        CcSectionLabel {
            visible: hintGroup.visible
            text: I18n.trFor("cast", "Can't find yours?")
        }

        CcGroup {
            id: hintGroup
            visible: panel.ready && panel.devices.length === 0 && !panel.session && panel.searchedLong

            CcListRow {
                iconName: "wifi"
                title: I18n.trFor("cast", "Guest networks often hide devices")
            }
        }

        // Quick settings
        CastOptions {
            id: choices
        }

        CcSectionLabel {
            visible: optionsGroup.visible
            text: I18n.trFor("cast", "Options")
        }

        CcGroup {
            id: optionsGroup
            visible: panel.ready && (!panel.isMedia || panel.showSettingsRow)

            CcListRow {
                visible: !panel.isMedia
                iconName: "speaker"
                title: I18n.trFor("cast", "Audio output")
                subtitle: panel.session ? I18n.trFor("cast", "Applies to the next cast") : ""

                DDropdown {
                    transientSurfaceTracker: panel.transientSurfaceTracker
                    anchors.verticalCenter: parent.verticalCenter
                    compactMode: true
                    dropdownWidth: CcMetrics.rowDropdownWidth
                    popupWidth: CcMetrics.configMenuWidth
                    alignPopupRight: true
                    options: choices.audioTargets.map(o => o.label)
                    currentValue: choices.labelOf(choices.audioTargets, panel.service?.pluginData.audioTarget ?? "tv")
                    onValueChanged: value => panel.service.setSetting("audioTarget", choices.valueOf(choices.audioTargets, value))
                }
            }

            CcListRow {
                id: qualityRow

                // Custom is set up in Settings; here it only shows while it's in use.
                readonly property bool custom: (panel.service?.pluginData.quality ?? "balanced") === "custom"

                visible: !panel.isMedia
                iconName: "high_quality"
                title: I18n.trFor("cast", "Quality")
                subtitle: {
                    if (panel.session)
                        return I18n.trFor("cast", "Applies to the next cast");
                    if (!custom)
                        return "";
                    const [lines, fps] = panel.service.quality();
                    return I18n.trFor("cast", "%1p, %2 fps").arg(lines).arg(fps);
                }
                trailingBadge: panel.service?.experimentalQuality ? I18n.trFor("cast", "Experimental") : ""
                trailingBadgeColor: Theme.warning

                DDropdown {
                    transientSurfaceTracker: panel.transientSurfaceTracker
                    anchors.verticalCenter: parent.verticalCenter
                    compactMode: true
                    dropdownWidth: CcMetrics.rowDropdownWidth
                    popupWidth: CcMetrics.configMenuWidth
                    alignPopupRight: true
                    options: choices.qualities.filter(o => o.value !== "custom" || qualityRow.custom).map(o => o.label)
                    optionIconMap: ({
                            [choices.labelOf(choices.qualities, "smooth")]: "science"
                        })
                    currentValue: choices.labelOf(choices.qualities, panel.service?.pluginData.quality ?? "balanced")
                    onValueChanged: value => panel.service.setSetting("quality", choices.valueOf(choices.qualities, value))
                }
            }

            CcListRow {
                visible: panel.showSettingsRow
                iconName: "settings"
                title: I18n.trFor("cast", "Cast settings")
                clickable: true
                onClicked: panel.openSettings()

                DIcon {
                    anchors.verticalCenter: parent.verticalCenter
                    name: "open_in_new"
                    size: Theme.iconSize
                    color: Theme.surfaceVariantText
                }
            }
        }
    }
}
