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
	$("refresh").addEventListener("click", requestTargets);
	$("target").addEventListener("change", onTargetChosen);
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
	if (payload.event === "error") {
		setStatus(payload.message, "error");
		$("target").innerHTML = '<option value="">Unavailable</option>';
		return;
	}
	if (payload.event !== "targets") return;

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
	settings = {
		bridge: $("bridge").value || settings.bridge || "",
		kind: kindLabel().toLowerCase(),
		target: opt.value,
		grouped_light: opt.dataset.groupedLight || undefined,
		name: opt.textContent.replace(/\s+\((on|off)\)$/, ""),
	};
	websocket.send(JSON.stringify({ event: "setSettings", context: actionInfo.context, payload: settings }));
	setStatus("Saved: " + settings.name, "ok");
}

// setStatus shows a message with a coloured dot: state is "busy", "ok",
// "error" or undefined for neutral.
function setStatus(text, state) {
	const el = $("status");
	el.textContent = text;
	el.className = "status" + (state ? " " + state : "");
}
