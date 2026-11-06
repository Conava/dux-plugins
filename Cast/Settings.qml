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
    readonly property bool requiredMissing: deps.some(d => d.required && !d.present)
    readonly property bool setupShown: !!service?.setupNeeded || missingIds.length > 0 || !!service?.helperUpdateAvailable
    // A soft tint, so the button to fix a row is what stands out.
    readonly property color requiredColor: Theme.blend(SettingsMetrics.rowColor, Theme.error, SettingsMetrics.highlightBlend)
    readonly property color recommendedColor: Theme.blend(SettingsMetrics.rowColor, Theme.warning, SettingsMetrics.highlightBlend)

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
    onWantsDevicesChanged: {
        service?.setDemand("settings", wantsDevices);
        if (wantsDevices)
            service?.recheck();
    }
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
                "label": I18n.trFor("cast", "Off"),
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
        property var service: null
        // Shown as a button; the command itself is only copied.
        property bool canRun: !!service?.canInstall
        property string runText: I18n.trFor("cast", "Install now")
        // Replaces running the command in a terminal.
        property var run: null
        // A short label in front, such as which hardware the command is for.
        property string label: ""
        // A web page instead of a command: it opens rather than being copied.
        readonly property bool isLink: /^https?:\/\//.test(command)

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

        DButton {
            anchors.verticalCenter: parent.verticalCenter
            visible: commandRow.canRun && !commandRow.isLink
            buttonHeight: Theme.buttonHeightXS
            iconName: "download"
            text: commandRow.runText
            onClicked: {
                if (commandRow.run)
                    commandRow.run();
                else
                    commandRow.service.runInTerminal(commandRow.command);
            }
        }

        DActionButton {
            id: copyButton
            anchors.verticalCenter: parent.verticalCenter
            iconName: commandRow.isLink ? "open_in_new" : "content_copy"
            iconSize: Theme.iconSizeSmall
            tooltipText: commandRow.isLink ? I18n.trFor("cast", "Open") : I18n.trFor("cast", "Copy")
            Accessible.name: tooltipText
            onClicked: {
                if (commandRow.isLink)
                    Qt.openUrlExternally(commandRow.command);
                else
                    Quickshell.execDetached(["dms", "cl", "copy", commandRow.command]);
            }
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
                        service: hints.service
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
        // Only in Setup, under the disclaimer; the full lists show the status.
        property bool actions: false

        iconName: modelData.present ? "check_circle" : "error"
        iconColor: modelData.present ? Theme.success : (modelData.required ? Theme.error : Theme.warning)
        title: modelData.package
        subtitle: I18n.trFor("cast", "Used for: %1").arg(service?.featuresText(modelData.features) ?? "")
        trailingBadge: modelData.present ? I18n.trFor("cast", "Installed") : I18n.trFor("cast", "Missing")
        trailingBadgeColor: modelData.present ? Theme.surfaceVariantText : (modelData.required ? Theme.error : Theme.warning)

        body: Column {
            width: parent.width
            spacing: Theme.spacingS
            visible: packageRow.actions && !packageRow.modelData.present
            height: visible ? implicitHeight : 0

            CommandRow {
                width: parent.width
                service: packageRow.service
                command: packageRow.service?.knownDistro ? packageRow.service.installCommand([packageRow.modelData.id]) : ""
            }

            PackageHints {
                width: parent.width
                service: packageRow.service
                ids: [packageRow.modelData.id]
            }
        }
    }

    // A port is only saved through its button, once it's checked.
    component PortField: SettingsTextFieldRow {
        id: portField

        property var service: null
        property string proto: ""
        property string saved: ""
        property string edited: saved
        property string problem: ""

        signal portSaved(string port)

        function savePort() {
            if (edited === saved)
                return;
            service.checkPort(proto, edited, found => {
                portField.problem = found;
                if (found)
                    return;
                portField.service.portSaved(portField.proto, portField.saved, portField.edited);
                portField.portSaved(portField.edited);
            });
        }

        iconName: "lan"
        value: saved
        isError: problem !== ""
        supportingText: problem
        validator: RegularExpressionValidator {
            regularExpression: /^[\d-]*$/
        }
        onSavedChanged: edited = saved
        onValueEdited: value => {
            edited = value;
            problem = "";
        }
        onAccepted: savePort()

        actions: DButton {
            visible: portField.edited !== portField.saved
            buttonHeight: Theme.buttonHeightXS
            iconName: "save"
            text: I18n.trFor("cast", "Save")
            onClicked: portField.savePort()
        }
    }

    // After a port changed: opens the new one and removes the old rule, where the firewall allows that.
    component PortOffer: SettingsRow {
        id: portOffer

        property var service: null
        property string proto: ""
        property string range: ""
        readonly property string previous: service?.previousPorts[proto] ?? ""
        readonly property bool canOffer: !!service?.canChangeRules && previous !== ""
        readonly property bool needsOpen: canOffer && service.neededPorts.some(p => p.proto === proto) && !service.portOpen(proto, range)
        readonly property bool canRemove: canOffer && !!service.portRule(proto, previous)

        visible: needsOpen || canRemove
        title: needsOpen ? I18n.trFor("cast", "Port %1 is not open").arg(range) : I18n.trFor("cast", "Port %1 is no longer used").arg(previous)

        body: Column {
            width: parent.width
            spacing: Theme.spacingS

            StyledText {
                width: parent.width
                text: portOffer.service?.firewallDisclaimer ?? ""
                wrapMode: Text.WordWrap
                font.pixelSize: Theme.fontSizeSmall
                color: Theme.error
            }

            Row {
                spacing: Theme.spacingS

                DButton {
                    visible: portOffer.needsOpen
                    buttonHeight: Theme.buttonHeightXS
                    iconName: "lock_open"
                    text: I18n.trFor("cast", "Open port %1").arg(portOffer.range)
                    onClicked: portOffer.service.openPort(portOffer.proto, portOffer.range)
                }

                DButton {
                    visible: portOffer.canRemove
                    buttonHeight: Theme.buttonHeightXS
                    iconName: "delete"
                    text: I18n.trFor("cast", "Remove the rule for port %1").arg(portOffer.previous)
                    backgroundColor: Theme.secondaryContainer
                    textColor: Theme.onSecondaryContainer
                    onClicked: portOffer.service.removePortRule(portOffer.proto, portOffer.previous)
                }
            }
        }
    }

    // Installing or updating the helper, or where to read how when the plugin can't do it.
    component HelperActions: Column {
        id: helperActions

        property var service: null
        property string runText: ""

        spacing: Theme.spacingS

        StyledText {
            width: parent.width
            visible: text !== ""
            text: helperActions.service?.helperDisclaimer ?? ""
            wrapMode: Text.WordWrap
            font.pixelSize: Theme.fontSizeSmall
            color: Theme.error
        }

        Row {
            spacing: Theme.spacingS

            CommandRow {
                anchors.verticalCenter: parent.verticalCenter
                service: helperActions.service
                canRun: !helperActions.service?.helperUpdating
                runText: helperActions.runText
                run: () => helperActions.service.installHelper()
                command: helperActions.service?.helperCommand ?? ""
            }

            StyledText {
                anchors.verticalCenter: parent.verticalCenter
                visible: !!helperActions.service?.helperUpdating
                text: I18n.trFor("cast", "Updating...")
                font.pixelSize: Theme.fontSizeSmall
                color: Theme.surfaceVariantText
            }

            DButton {
                anchors.verticalCenter: parent.verticalCenter
                visible: !helperActions.service?.helperCommand
                buttonHeight: Theme.buttonHeightXS
                iconName: "help"
                text: I18n.trFor("cast", "Help")
                backgroundColor: Theme.secondaryContainer
                textColor: Theme.onSecondaryContainer
                onClicked: Qt.openUrlExternally(helperActions.service.installGuide)
            }
        }
    }

    // The firewall step: which ports and why, and the command where the firewall is known.
    component FirewallRow: SettingsRow {
        id: firewallRow

        property var service: null
        readonly property string kind: service?.firewall ?? ""
        readonly property bool done: !!service?.firewallReady
        readonly property bool known: kind === "firewalld" || kind === "ufw"
        readonly property var lines: (service?.neededPorts ?? []).concat(kind === "firewalld" ? [{
                    "label": "mDNS",
                    "use": I18n.trFor("cast", "Device discovery")
                }] : [])

        iconName: kind === "none" ? "info" : done ? "check_circle" : "shield"
        title: {
            if (kind === "none")
                return I18n.trFor("cast", "No firewall detected");
            if (done)
                return I18n.trFor("cast", "Firewall");
            return known ? I18n.trFor("cast", "Open the ports in %1").arg(kind) : I18n.trFor("cast", "Open the ports in your firewall");
        }
        subtitle: {
            if (kind === "none")
                return I18n.trFor("cast", "If your network is filtered elsewhere, open these ports there");
            if (done)
                return I18n.trFor("cast", "The ports are open");
            if (known)
                return I18n.trFor("cast", "These ports must be open for cast devices to reach this computer");
            return I18n.trFor("cast", "Open these ports in your firewall");
        }

        body: Column {
            width: parent.width
            spacing: Theme.spacingS

            Repeater {
                model: firewallRow.lines

                StyledText {
                    required property var modelData

                    width: parent.width
                    text: modelData.label + ": " + modelData.use
                    wrapMode: Text.WordWrap
                    font.pixelSize: Theme.fontSizeSmall
                    color: firewallRow.subtitleColor
                }
            }

            StyledText {
                width: parent.width
                visible: !firewallRow.done && !!firewallRow.service?.canUseSudo && !!firewallRow.service?.firewallCommand
                text: firewallRow.service?.firewallDisclaimer ?? ""
                wrapMode: Text.WordWrap
                font.pixelSize: Theme.fontSizeSmall
                color: Theme.error
            }

            CommandRow {
                width: parent.width
                service: firewallRow.service
                canRun: !firewallRow.done && !!firewallRow.service?.canUseSudo
                runText: I18n.trFor("cast", "Apply now")
                run: () => firewallRow.service.runInTerminal(firewallRow.service.firewallCommand, () => firewallRow.service.checkFirewall())
                command: firewallRow.done ? "" : (firewallRow.service?.firewallCommand ?? "")
            }

            DButton {
                visible: !firewallRow.known
                buttonHeight: Theme.buttonHeightXS
                iconName: "help"
                text: I18n.trFor("cast", "Help")
                backgroundColor: Theme.secondaryContainer
                textColor: Theme.onSecondaryContainer
                onClicked: Qt.openUrlExternally(firewallRow.service.firewallGuide)
            }

            DButton {
                visible: !firewallRow.done
                buttonHeight: Theme.buttonHeightXS
                iconName: "check"
                text: I18n.trFor("cast", "Ports are open")
                backgroundColor: Theme.secondaryContainer
                textColor: Theme.onSecondaryContainer
                onClicked: firewallRow.service.confirmFirewall()
            }
        }
    }

    Column {
        width: parent.width
        spacing: 0

        // Only what's left to do: red is needed for Cast to work, yellow is recommended.
        SettingsSectionLabel {
            visible: root.setupShown
            text: I18n.trFor("cast", "Setup")
        }

        SettingsGroup {
            visible: !!root.service?.helperUpdateAvailable
            slotColor: root.recommendedColor

            SettingsRow {
                iconName: "upgrade"
                iconColor: Theme.warning
                title: I18n.trFor("cast", "New Cast helper available")
                subtitle: I18n.trFor("cast", "Updating is highly recommended. Version %1 remains compatible, but some features may be unavailable").arg(root.service?.helperVersion ?? "")

                body: HelperActions {
                    width: parent.width
                    service: root.service
                    runText: I18n.trFor("cast", "Update now")
                }
            }
        }

        SettingsGroup {
            id: installGroup

            readonly property string command: root.missingIds.length > 0 ? (root.service?.installCommand(root.missingIds) ?? "") : ""

            visible: command !== ""

            SettingsRow {
                iconName: "download"
                title: I18n.trFor("cast", "Install all missing packages")
                subtitle: root.service?.knownDistro ? "" : I18n.trFor("cast", "Install with your package manager: %1").arg(installGroup.command)

                body: Column {
                    width: parent.width
                    spacing: Theme.spacingS

                    StyledText {
                        width: parent.width
                        visible: !!root.service?.canInstall
                        text: root.service?.installDisclaimer ?? ""
                        wrapMode: Text.WordWrap
                        font.pixelSize: Theme.fontSizeSmall
                        color: Theme.error
                    }

                    CommandRow {
                        service: root.service
                        command: root.service?.knownDistro ? installGroup.command : ""
                    }

                    DButton {
                        visible: !root.service?.knownDistro
                        buttonHeight: Theme.buttonHeightXS
                        iconName: "help"
                        text: I18n.trFor("cast", "Help")
                        backgroundColor: Theme.secondaryContainer
                        textColor: Theme.onSecondaryContainer
                        onClicked: Qt.openUrlExternally(root.service.dependenciesGuide)
                    }
                }
            }
        }

        SettingsGroup {
            id: requiredGroup

            readonly property bool firewallOpen: !root.service || root.service.firewall === "" || root.service.firewallReady

            visible: !!root.service?.helperProblem || root.requiredMissing || !firewallOpen
            slotColor: root.requiredColor

            SettingsRow {
                visible: !!root.service?.helperProblem
                iconName: "memory"
                iconColor: Theme.error
                title: root.service?.helperOutdated ? I18n.trFor("cast", "Cast helper is outdated") : I18n.trFor("cast", "Cast helper not installed")
                subtitle: I18n.trFor("cast", "Required for all casting features")

                body: HelperActions {
                    width: parent.width
                    service: root.service
                    runText: root.service?.helperOutdated ? I18n.trFor("cast", "Update now") : I18n.trFor("cast", "Install now")
                }
            }

            Repeater {
                model: root.deps.filter(d => d.required && !d.present)

                PackageRow {
                    service: root.service
                    iconColor: Theme.error
                    trailingBadge: I18n.trFor("cast", "Required")
                    actions: true
                }
            }

            FirewallRow {
                visible: !requiredGroup.firewallOpen
                service: root.service
                iconColor: Theme.error
            }
        }

        SettingsGroup {
            visible: missingRecommended.count > 0
            slotColor: root.recommendedColor

            Repeater {
                id: missingRecommended
                model: root.deps.filter(d => !d.required && !d.present)

                PackageRow {
                    service: root.service
                    trailingBadge: I18n.trFor("cast", "Recommended")
                    actions: true
                }
            }
        }

        SettingsGroup {
            visible: root.setupShown

            SettingsRow {
                DButton {
                    anchors.verticalCenter: parent.verticalCenter
                    buttonHeight: Theme.buttonHeightXS
                    iconName: "refresh"
                    text: I18n.trFor("cast", "Check again")
                    backgroundColor: Theme.secondaryContainer
                    textColor: Theme.onSecondaryContainer
                    onClicked: {
                        root.service.restartHelper();
                        root.service.checkFirewall();
                    }
                }
            }
        }

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
                    return Object.values(byId).sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));
                }
                readonly property var labels: root.service?.deviceLabels(deviceChoices) ?? []
                readonly property string favoriteLabel: labels[deviceChoices.findIndex(d => d.id === root.value("favoriteDevice", ""))] ?? favoriteName

                options: {
                    const names = labels.slice();
                    if (favoriteLabel && !names.includes(favoriteLabel))
                        names.unshift(favoriteLabel);
                    return [noneLabel].concat(names);
                }
                currentValue: favoriteLabel || noneLabel
                onValueChanged: value => {
                    const device = deviceChoices[labels.indexOf(value)];
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
                id: intervalRow

                readonly property string interval: String(root.value("slideshowInterval", "8"))
                readonly property string customLabel: I18n.trFor("cast", "Custom...")
                // Custom stays open once picked, and for a time that isn't one of the choices.
                property bool customPicked: false
                readonly property bool custom: customPicked || !choices.intervals.some(o => o.value === interval)

                iconName: "slideshow"
                text: I18n.trFor("cast", "Slideshow interval")
                options: choices.intervals.map(o => o.label).concat([customLabel])
                currentValue: custom ? customLabel : choices.labelOf(choices.intervals, interval)
                onValueChanged: value => {
                    customPicked = value === customLabel;
                    if (!customPicked)
                        root.save("slideshowInterval", choices.valueOf(choices.intervals, value));
                }
            }

            SettingsTextFieldRow {
                visible: intervalRow.custom
                iconName: "timer"
                text: I18n.trFor("cast", "Seconds per photo")
                value: intervalRow.custom && !intervalRow.customPicked ? intervalRow.interval : ""
                validator: IntValidator {
                    bottom: 1
                    top: 3600
                }
                onEditingFinished: value => {
                    if (parseInt(value) > 0)
                        root.save("slideshowInterval", String(parseInt(value)));
                }
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
        }

        SettingsSectionLabel {
            text: I18n.trFor("cast", "Firewall and ports")
        }

        SettingsGroup {
            FirewallRow {
                visible: !!root.service && root.service.firewallReady
                service: root.service
            }

            PortField {
                service: root.service
                proto: "tcp"
                saved: root.port
                text: I18n.trFor("cast", "Media port (TCP)")
                onPortSaved: port => root.save("mediaPort", port)
            }

            PortOffer {
                service: root.service
                proto: "tcp"
                range: root.port
            }

            PortField {
                service: root.service
                proto: "udp"
                saved: root.airplayPorts
                text: I18n.trFor("cast", "AirPlay ports (UDP)")
                onPortSaved: port => root.save("airplayPorts", port)
            }

            PortOffer {
                service: root.service
                proto: "udp"
                range: root.airplayPorts
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
                    if (root.service.helperPath && root.service.helperOutdated)
                        return I18n.trFor("cast", "Outdated");
                    if (root.service.helperPath && root.service.helperVersion)
                        return root.service.helperPath + " · " + root.service.helperVersion;
                    return root.service.helperPath || I18n.trFor("cast", "Not installed");
                }
            }

            SettingsTextFieldRow {
                iconName: "folder_open"
                text: I18n.trFor("cast", "Helper path")
                description: I18n.trFor("cast", "Only needed for a helper outside PATH and ~/.local/share/dms/cast/bin")
                placeholderText: "cast-helper"
                value: root.value("helperPath", "")
                onEditingFinished: value => {
                    if (value.trim() !== root.value("helperPath", ""))
                        root.save("helperPath", value.trim());
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

        SettingsSectionLabel {
            text: I18n.trFor("cast", "Troubleshooting")
        }

        SettingsGroup {
            SettingsToggleRow {
                iconName: "manage_search"
                text: I18n.trFor("cast", "Detailed logging")
                description: I18n.trFor("cast", "Records more detail in the logs. Enable it before reproducing an issue")
                checked: root.value("detailedLogging", false)
                onToggled: checked => root.save("detailedLogging", checked)
            }

            SettingsRow {
                iconName: "bug_report"
                title: I18n.trFor("cast", "Bug report")
                subtitle: I18n.trFor("cast", "Collects logs and system details for a bug report. It may contain device names, local addresses and file names. Review it before sharing, and attach a screenshot for visual issues")

                body: Row {
                    spacing: Theme.spacingS

                    DButton {
                        buttonHeight: Theme.buttonHeightXS
                        iconName: "description"
                        text: I18n.trFor("cast", "Open logs")
                        backgroundColor: Theme.secondaryContainer
                        textColor: Theme.onSecondaryContainer
                        onClicked: root.service.openReport()
                    }

                    DButton {
                        buttonHeight: Theme.buttonHeightXS
                        iconName: "content_copy"
                        text: I18n.trFor("cast", "Copy report")
                        backgroundColor: Theme.secondaryContainer
                        textColor: Theme.onSecondaryContainer
                        onClicked: root.service.copyReport()
                    }
                }
            }
        }
    }
}
