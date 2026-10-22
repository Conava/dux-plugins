pragma ComponentBehavior: Bound

import QtQuick
import Quickshell
import qs.Common
import qs.DCommon.Widgets
import qs.Modules.Plugins
import qs.Modules.Settings.Widgets

PluginSettings {
    id: root

    // More pixels per second than 1080p at 30 fps outruns some devices
    // (1080p60 does, 720p60 doesn't); it's marked rather than hidden.
    readonly property bool experimental: (effective[0] === "1080" ? 1920 * 1080 : 1280 * 720) * parseInt(effective[1]) > 1920 * 1080 * 30
    // Custom is offered once a custom configuration was saved from Advanced.
    readonly property bool hasCustom: String(value("resolution", "")) !== ""

    pluginId: "cast"

    // loadValue isn't reactive; bindings read rev so they update after a save.
    property int rev: 0
    property bool advancedOpen: false
    readonly property var service: pluginService?.pluginDaemonInstances["cast"] ?? null
    readonly property var devices: service?.devices ?? []
    readonly property string quality: value("quality", "balanced")
    // Resolution, frame rate and bitrate the quality preset stands for.
    readonly property var effective: {
        switch (quality) {
        case "smooth":
            return ["1080", "60", 0];
        case "low":
            return ["720", "30", 0];
        case "custom":
            return [value("resolution", "1080"), value("frameRate", "30"), value("bitrate", 0)];
        }
        return ["1080", "30", 0];
    }
    readonly property string port: value("mediaPort", "47830")
    readonly property string airplayPorts: value("airplayPorts", "47840-47849")
    readonly property var deps: service?.deps ?? []
    readonly property var missingIds: deps.filter(d => !d.present).map(d => d.id)

    function value(key, fallback) {
        rev;
        return loadValue(key, fallback);
    }

    function save(key, v) {
        saveValue(key, v);
        rev++;
    }

    // Changing one of the custom values switches the preset to Custom, keeping the others.
    function saveCustom(key, v) {
        if (quality !== "custom") {
            saveValue("resolution", effective[0]);
            saveValue("frameRate", effective[1]);
            saveValue("bitrate", effective[2]);
            saveValue("quality", "custom");
        }
        save(key, v);
    }

    // The device list is live only while the helper runs, so this page asks for discovery while it's on screen.
    readonly property bool wantsDevices: !!service && visible && (Window.window?.visible ?? false)
    onWantsDevicesChanged: service?.setDemand("settings", wantsDevices)
    Component.onDestruction: service?.setDemand("settings", false)

    Connections {
        target: root.pluginService
        function onPluginDataChanged(changedId) {
            if (changedId === root.pluginId)
                root.rev++;
        }
    }

    // A copy of the choices in CastWidget.qml: the shell recompiles only a
    // plugin's own entry files on reload, so a shared sibling file goes stale.
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

        readonly property var codecs: [
            {
                "label": I18n.trFor("cast", "Automatic"),
                "value": "auto"
            },
            {
                "label": "H.264",
                "value": "h264"
            },
            {
                "label": "HEVC",
                "value": "hevc"
            }
        ]

        readonly property var intervals: [
            {
                "label": I18n.trFor("cast", "Manual"),
                "value": "0"
            }
        ].concat([5, 8, 15, 30].map(n => ({
                        "label": I18n.trFor("cast", "%1 seconds").arg(n),
                        "value": String(n)
                    })))

        function labelOf(list, value) {
            return (list.find(o => o.value === value) ?? list[0]).label;
        }

        function valueOf(list, label) {
            return (list.find(o => o.label === label) ?? list[0]).value;
        }
    }

    CastOptions {
        id: choices
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
            color: Theme.surfaceVariantText
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

    // One package: its name here, what it's for, and whether it's installed.
    component PackageRow: SettingsRow {
        id: packageRow

        required property var modelData
        property var service: null

        iconName: modelData.present ? "check_circle" : "error"
        iconColor: modelData.present ? Theme.success : (modelData.required ? Theme.error : Theme.warning)
        title: modelData.package
        subtitle: I18n.trFor("cast", "Used for: %1").arg(service?.featuresText(modelData.features) ?? "")
        trailingBadge: modelData.present ? I18n.trFor("cast", "Installed") : I18n.trFor("cast", "Missing")
        trailingBadgeColor: modelData.present ? Theme.surfaceVariantText : (modelData.required ? Theme.error : Theme.warning)

        body: PackageHints {
            width: parent.width
            service: packageRow.service
            ids: packageRow.modelData.present ? [] : [packageRow.modelData.id]
        }
    }

    Column {
        width: parent.width
        spacing: 0

        SettingsGroup {
            SettingsDropdownRow {
                id: defaultDevice

                readonly property string favoriteName: root.value("favoriteName", "")
                readonly property string noneLabel: I18n.trFor("cast", "None")

                iconName: "cast"
                text: I18n.trFor("cast", "Default device")
                // Devices seen before count too, so one that is switched off can be picked.
                readonly property var deviceChoices: {
                    const byId = {};
                    for (const [id, d] of Object.entries(root.service?.knownDevices ?? {}))
                        byId[id] = {
                            "id": id,
                            "name": d.name
                        };
                    for (const d of root.devices)
                        byId[d.id] = d;
                    return Object.values(byId).sort((a, b) => a.name.localeCompare(b.name));
                }

                options: {
                    const names = deviceChoices.map(d => d.name);
                    if (favoriteName && !names.includes(favoriteName))
                        names.unshift(favoriteName);
                    return [noneLabel].concat(names);
                }
                currentValue: favoriteName || noneLabel
                onValueChanged: value => {
                    const device = deviceChoices.find(d => d.name === value);
                    if (value === noneLabel || device) {
                        root.saveValue("favoriteDevice", device?.id ?? "");
                        root.save("favoriteName", device?.name ?? "");
                    }
                }
            }

            SettingsDropdownRow {
                iconName: "speaker"
                text: I18n.trFor("cast", "Audio output")
                options: choices.audioTargets.map(o => o.label)
                currentValue: choices.labelOf(choices.audioTargets, root.value("audioTarget", "tv"))
                onValueChanged: value => root.save("audioTarget", choices.valueOf(choices.audioTargets, value))
            }
        }

        SettingsSectionLabel {
            text: I18n.trFor("cast", "Screen mirroring")
        }

        SettingsGroup {
            SettingsDropdownRow {
                iconName: "high_quality"
                text: I18n.trFor("cast", "Quality")
                trailingBadge: root.experimental ? I18n.trFor("cast", "Experimental") : ""
                trailingBadgeColor: Theme.warning
                optionIconMap: ({
                        [choices.labelOf(choices.qualities, "smooth")]: "science"
                    })
                options: choices.qualities.filter(o => o.value !== "custom" || root.hasCustom).map(o => o.label)
                currentValue: choices.labelOf(choices.qualities, root.quality)
                onValueChanged: value => root.save("quality", choices.valueOf(choices.qualities, value))
            }
        }

        SettingsSectionLabel {
            text: I18n.trFor("cast", "Advanced")
            collapsible: true
            expanded: root.advancedOpen
            onToggleRequested: root.advancedOpen = !root.advancedOpen
        }

        SettingsGroup {
            visible: root.advancedOpen

            SettingsButtonGroupRow {
                iconName: "aspect_ratio"
                text: I18n.trFor("cast", "Resolution")
                model: [I18n.trFor("cast", "%1p").arg(1080), I18n.trFor("cast", "%1p").arg(720)]
                values: ["1080", "720"]
                value: root.effective[0]
                fallbackValue: "1080"
                onValueSelected: value => {
                    if (value !== root.effective[0])
                        root.saveCustom("resolution", value);
                }
            }

            SettingsButtonGroupRow {
                iconName: "speed"
                text: I18n.trFor("cast", "Frame rate")
                trailingBadge: root.experimental ? I18n.trFor("cast", "Experimental") : ""
                trailingBadgeColor: Theme.warning
                model: [I18n.trFor("cast", "%1 fps").arg(60), I18n.trFor("cast", "%1 fps").arg(30)]
                values: ["60", "30"]
                value: root.effective[1]
                fallbackValue: "30"
                onValueSelected: value => {
                    if (value !== root.effective[1])
                        root.saveCustom("frameRate", value);
                }
            }

            SettingsSliderRow {
                iconName: "network_check"
                text: I18n.trFor("cast", "Bitrate")
                minimumLabel: I18n.trFor("cast", "Automatic")
                minimum: 0
                maximum: 40
                unit: I18n.trFor("cast", "Mbit/s")
                value: root.effective[2]
                onSliderDragFinished: finalValue => {
                    if (finalValue !== root.effective[2])
                        root.saveCustom("bitrate", finalValue);
                }
            }

            SettingsDropdownRow {
                iconName: "movie_filter"
                text: I18n.trFor("cast", "Video codec")
                options: choices.codecs.map(o => o.label)
                currentValue: choices.labelOf(choices.codecs, root.value("codec", "auto"))
                onValueChanged: value => root.save("codec", choices.valueOf(choices.codecs, value))
            }

            SettingsSliderRow {
                iconName: "timer"
                text: I18n.trFor("cast", "Latency")
                minimum: 100
                maximum: 1000
                step: 50
                unit: I18n.trFor("cast", "ms")
                value: root.value("latency", 200)
                onSliderDragFinished: finalValue => root.save("latency", finalValue)

                DActionButton {
                    anchors.verticalCenter: parent.verticalCenter
                    buttonSize: Theme.iconButtonSize
                    iconName: "info"
                    iconSize: Theme.iconSizeMedium
                    iconColor: Theme.surfaceVariantText
                    tooltipText: I18n.trFor("cast", "Lower reacts faster; higher plays smoother on a weak network")
                }
            }
        }

        SettingsSectionLabel {
            text: I18n.trFor("cast", "Media playback")
        }

        SettingsGroup {
            SettingsDropdownRow {
                iconName: "slideshow"
                text: I18n.trFor("cast", "Slideshow interval")
                options: choices.intervals.map(o => o.label)
                currentValue: choices.labelOf(choices.intervals, String(root.value("slideshowInterval", "8")))
                onValueChanged: value => root.save("slideshowInterval", choices.valueOf(choices.intervals, value))
            }

            SettingsToggleRow {
                iconName: "photo_size_select_large"
                text: I18n.trFor("cast", "Compress large photos")
                checked: root.value("shrinkPhotos", false)
                onToggled: checked => root.save("shrinkPhotos", checked)
            }

            SettingsToggleRow {
                iconName: "autorenew"
                text: I18n.trFor("cast", "Convert to compatible formats")
                checked: root.value("convertVideos", true)
                onToggled: checked => root.save("convertVideos", checked)
            }

            SettingsTextFieldRow {
                iconName: "lan"
                text: I18n.trFor("cast", "Port")
                value: root.port
                validator: IntValidator {
                    bottom: 1024
                    top: 65535
                }
                onEditingFinished: value => {
                    if (value && value !== root.port)
                        root.save("mediaPort", value);
                }
            }

            SettingsRow {
                body: CommandRow {
                    width: parent.width
                    command: "sudo ufw allow from 192.168.0.0/16 to any port " + root.port + " proto tcp"
                }
            }
        }

        SettingsSectionLabel {
            text: I18n.trFor("cast", "AirPlay")
        }

        SettingsGroup {
            SettingsTextFieldRow {
                iconName: "lan"
                text: I18n.trFor("cast", "UDP ports")
                value: root.airplayPorts
                validator: RegularExpressionValidator {
                    regularExpression: /^\d{4,5}(-\d{4,5})?$/
                }
                onEditingFinished: value => {
                    if (value && value !== root.airplayPorts)
                        root.save("airplayPorts", value);
                }
            }

            SettingsRow {
                body: CommandRow {
                    width: parent.width
                    command: "sudo ufw allow from 192.168.0.0/16 to any port " + root.airplayPorts.replace("-", ":") + " proto udp"
                }
            }
        }

        SettingsSectionLabel {
            text: I18n.trFor("cast", "Bar")
        }

        SettingsGroup {
            SettingsButtonGroupRow {
                iconName: "visibility"
                text: I18n.trFor("cast", "Show the pill")
                model: [I18n.trFor("cast", "Always"), I18n.trFor("cast", "While casting")]
                values: [false, true]
                value: root.value("pillOnlyWhileCasting", false)
                fallbackValue: false
                onValueSelected: value => root.save("pillOnlyWhileCasting", value)
            }

            SettingsToggleRow {
                iconName: "label"
                text: I18n.trFor("cast", "Show the device name")
                description: I18n.trFor("cast", "Horizontal bars only")
                checked: root.value("showDeviceName", true)
                onToggled: checked => root.save("showDeviceName", checked)
            }
        }

        SettingsSectionLabel {
            text: I18n.trFor("cast", "Helper")
        }

        SettingsGroup {
            SettingsRow {
                iconName: "memory"
                title: "cast-helper"
                subtitle: {
                    if (!root.service?.helperResolved)
                        return "";
                    return root.service.helperPath || I18n.trFor("cast", "Not installed");
                }

                body: CommandRow {
                    width: parent.width
                    command: root.service?.helperResolved && !root.service.helperPath ? root.service.helperDownloadCommand : ""
                }
            }

            SettingsRow {
                DButton {
                    anchors.verticalCenter: parent.verticalCenter
                    buttonHeight: Theme.buttonHeightXS
                    iconName: "open_in_new"
                    text: I18n.trFor("cast", "Installation guide")
                    backgroundColor: Theme.secondaryContainer
                    textColor: Theme.onSecondaryContainer
                    onClicked: Qt.openUrlExternally(root.service.installGuide)
                }
            }
        }

        // Every package the helper can use, from its check report.
        SettingsSectionLabel {
            visible: !!root.service?.helperPath
            text: I18n.trFor("cast", "Packages")
        }

        SettingsGroup {
            visible: !!root.service?.helperPath && root.deps.length > 0

            SettingsRow {
                readonly property bool requiredMissing: root.deps.some(d => d.required && !d.present)
                readonly property color tone: requiredMissing ? Theme.error : root.missingIds.length > 0 ? Theme.primary : Theme.success

                rowColor: Theme.blend(SettingsMetrics.rowColor, tone, SettingsMetrics.highlightBlend)
                iconName: requiredMissing ? "error" : "check_circle"
                iconColor: tone
                title: {
                    if (requiredMissing)
                        return I18n.trFor("cast", "Required packages are missing");
                    if (root.missingIds.length > 0)
                        return I18n.trFor("cast", "All required packages are installed");
                    return I18n.trFor("cast", "Everything is installed");
                }
                subtitle: {
                    if (root.missingIds.length === 0)
                        return "";
                    if (!root.service?.knownDistro)
                        return I18n.trFor("cast", "Install these with your package manager");
                    return requiredMissing ? "" : I18n.trFor("cast", "Install the recommended packages for every feature");
                }

                body: CommandRow {
                    width: parent.width
                    command: root.missingIds.length > 0 ? (root.service?.installCommand(root.missingIds) ?? "") : ""
                }
            }

            SettingsRow {
                DButton {
                    anchors.verticalCenter: parent.verticalCenter
                    buttonHeight: Theme.buttonHeightXS
                    iconName: "refresh"
                    text: I18n.trFor("cast", "Check again")
                    backgroundColor: Theme.secondaryContainer
                    textColor: Theme.onSecondaryContainer
                    onClicked: root.service.recheck()
                }
            }
        }

        SettingsSectionLabel {
            visible: !!root.service?.helperPath
            text: I18n.trFor("cast", "Required packages")
        }

        SettingsGroup {
            visible: !!root.service?.helperPath

            SettingsRow {
                visible: root.deps.length === 0
                iconName: "hourglass_top"
                title: I18n.trFor("cast", "Checking packages...")
            }

            Repeater {
                model: root.deps.filter(d => d.required)

                PackageRow {
                    service: root.service
                }
            }
        }

        SettingsSectionLabel {
            visible: recommended.count > 0
            text: I18n.trFor("cast", "Recommended packages")
        }

        SettingsGroup {
            visible: recommended.count > 0

            Repeater {
                id: recommended
                model: root.deps.filter(d => !d.required)

                PackageRow {
                    service: root.service
                }
            }
        }
    }
}
