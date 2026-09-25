// Property inspector logic. The host calls connectOpenActionSocket with the
// WebSocket port and the identity of this inspector; we register, ask the
// plugin for the list of targets, and store the user's choice as the
// button's settings.
//
// Messages to the plugin:   {"event":"sendToPlugin","action","context","payload":{...}}
// Messages from the plugin: {"event":"sendToPropertyInspector","payload":{...}}
// Settings are stored with:  {"event":"setSettings","context","payload":{...}}

"use strict";

let websocket = null;
let actionInfo = null; // {action, context, payload:{settings,...}}
let settings = {};     // the button's current settings

const $ = (id) => document.getElementById(id);

// Both names are defined: OpenAction's and the legacy Elgato one, whichever
// the host calls.
function connectOpenActionSocket(port, uuid, registerEvent, info, inActionInfo) {
	actionInfo = typeof inActionInfo === "string" ? JSON.parse(inActionInfo) : inActionInfo;
	settings = (actionInfo.payload && actionInfo.payload.settings) || {};

	websocket = new WebSocket("ws://127.0.0.1:" + port);
	websocket.onopen = () => {
		websocket.send(JSON.stringify({ event: registerEvent, uuid: uuid }));
		requestTargets();
	};
	websocket.onmessage = (msg) => {
		const data = JSON.parse(msg.data);
		if (data.event === "sendToPropertyInspector") {
			onPluginMessage(data.payload || {});
		} else if (data.event === "didReceiveSettings") {
			settings = (data.payload && data.payload.settings) || {};
		}
	};
	websocket.onclose = () => setStatus("Disconnected from OpenDeck", "error");

	$("target-label").textContent = kindLabel();
	$("label").options[0].textContent = kindLabel() + " name";
	$("refresh").addEventListener("click", requestTargets);
	$("target").addEventListener("change", onTargetChosen);
	$("label").addEventListener("change", onLabelChanged);
	$("custom-label").addEventListener("input", onLabelChanged);
	showLabelControls();
	$("bridge").addEventListener("change", () => {
		settings.bridge = $("bridge").value;
		requestTargets();
	});
}
const connectElgatoStreamDeckSocket = connectOpenActionSocket;

// kindLabel derives "Light", "Room" or "Zone" from the action UUID.
function kindLabel() {
	const name = (actionInfo.action || "").split(".").pop(); // "toggle-light"
	const kind = name.replace("toggle-", "");
	return kind ? kind[0].toUpperCase() + kind.slice(1) : "Target";
}

function sendToPlugin(payload) {
	websocket.send(JSON.stringify({
		event: "sendToPlugin",
		action: actionInfo.action,
		context: actionInfo.context,
		payload: payload,
	}));
}

function requestTargets() {
	setStatus("Loading from the bridge…", "busy");
	$("target").disabled = true;
	sendToPlugin({ event: "listTargets", bridge: settings.bridge || "" });
}

function onPluginMessage(payload) {
	if (payload.event === "pairing") {
		onPairingProgress(payload);
		return;
	}
	if (payload.event === "error") {
		if (payload.code === "not_paired") {
			showPairing(true);
			return;
		}
		setStatus(payload.message, "error");
		$("target").innerHTML = '<option value="">Unavailable</option>';
		return;
	}
	if (payload.event !== "targets") return;
	showPairing(false);

	// Bridge selector: only shown when more than one bridge is paired.
	const bridgeSelect = $("bridge");
	bridgeSelect.innerHTML = "";
	for (const b of payload.bridges) {
		const opt = document.createElement("option");
		opt.value = b.id;
		opt.textContent = (b.name || b.id) + (b.default ? " (default)" : "");
		opt.selected = b.id === payload.bridge;
		bridgeSelect.appendChild(opt);
	}
	$("bridge-row").hidden = payload.bridges.length < 2;

	// Target selector.
	const select = $("target");
	select.innerHTML = "";
	const placeholder = document.createElement("option");
	placeholder.value = "";
	placeholder.textContent = "— choose a " + payload.kind + " —";
	select.appendChild(placeholder);
	for (const item of payload.items) {
		const opt = document.createElement("option");
		opt.value = item.id;
		opt.textContent = item.name + (item.on ? "  (on)" : "  (off)");
		opt.dataset.groupedLight = item.grouped_light || "";
		opt.selected = item.id === settings.target;
		select.appendChild(opt);
	}
	select.disabled = false;
	setStatus(payload.items.length + " " + payload.kind + (payload.items.length === 1 ? "" : "s") + " found", "ok");
}

function onTargetChosen() {
	const select = $("target");
	const opt = select.options[select.selectedIndex];
	if (!opt || !opt.value) return;
	settings.bridge = $("bridge").value || settings.bridge || "";
	settings.kind = kindLabel().toLowerCase();
	settings.target = opt.value;
	settings.grouped_light = opt.dataset.groupedLight || undefined;
	settings.name = opt.textContent.replace(/\s+\((on|off)\)$/, "");
	saveSettings("Saved: " + settings.name);
}

// Key text controls. The choice is stored with the other settings; the
// plugin applies it when the settings arrive.
function showLabelControls() {
	$("label").value = settings.label || "name";
	$("custom-label").value = settings.custom_label || "";
	$("custom-row").hidden = $("label").value !== "custom";
}

function onLabelChanged() {
	settings.label = $("label").value;
	settings.custom_label = $("custom-label").value;
	$("custom-row").hidden = settings.label !== "custom";
	if (settings.label !== "custom") delete settings.custom_label;
	if (!settings.target) return; // nothing to show yet; saved with the target later
	saveSettings("Saved");
}

function saveSettings(message) {
	websocket.send(JSON.stringify({ event: "setSettings", context: actionInfo.context, payload: settings }));
	setStatus(message, "ok");
}

// setStatus shows a message with a coloured dot: state is "busy", "ok",
// "error" or undefined for neutral.
function setStatus(text, state) {
	const el = $("status");
	el.textContent = text;
	el.className = "status" + (state ? " " + state : "");
}

// ---- Pairing from the panel ----
// The plugin runs the same flow as `hue auth` and reports each stage.

function showPairing(show) {
	$("pair").hidden = !show;
	$("configure").hidden = show;
}

function startPairing() {
	$("pair-button").disabled = true;
	$("pair-countdown").hidden = true;
	setPairStatus("Starting…", "busy");
	sendToPlugin({ event: "pair", address: $("pair-address").value.trim() });
}

function onPairingProgress(payload) {
	const countdown = $("pair-countdown");
	switch (payload.stage) {
		case "searching":
		case "found":
			setPairStatus(payload.message, "busy");
			break;
		case "waiting":
			setPairStatus(payload.message, "busy");
			countdown.hidden = false;
			countdown.textContent = payload.seconds_left + " s";
			break;
		case "paired":
			countdown.hidden = true;
			setPairStatus(payload.message, "ok");
			$("pair-button").disabled = false;
			// Switch to the normal view and load the targets.
			setTimeout(requestTargets, 800);
			break;
		case "error":
			countdown.hidden = true;
			setPairStatus(payload.message, "error");
			$("pair-button").disabled = false;
			break;
	}
}

function setPairStatus(text, state) {
	const el = $("pair-status");
	el.textContent = text;
	el.className = "status" + (state ? " " + state : "");
}
