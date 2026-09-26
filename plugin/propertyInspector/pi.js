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
let lastTargets = null; // the last "targets" payload, re-filtered when the group changes

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
	$("group-row").hidden = !isScene();
	$("dynamic-row").hidden = !isScene();
	$("scene-group").addEventListener("change", () => renderTargets());
	$("dynamic").addEventListener("change", onDynamicChanged);
	$("dynamic").checked = !!settings.dynamic;
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

// kindLabel derives "Light", "Room", "Zone" or "Scene" from the action UUID.
function kindLabel() {
	const name = (actionInfo.action || "").split(".").pop(); // "toggle-light" or "scene"
	const kind = name.replace("toggle-", "");
	return kind ? kind[0].toUpperCase() + kind.slice(1) : "Target";
}

function isScene() {
	return kindLabel() === "Scene";
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
	if (payload.event === "bridges") {
		onBridgesFound(payload);
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

	lastTargets = payload;
	if (isScene()) {
		// Room or zone selector, filled from the groups the plugin sent; the
		// button's stored group, else the first, is selected.
		const gs = $("scene-group");
		gs.innerHTML = "";
		for (const g of payload.groups || []) {
			const opt = document.createElement("option");
			opt.value = g.id;
			opt.textContent = g.name + "  (" + g.kind + ")";
			opt.selected = g.id === settings.group;
			gs.appendChild(opt);
		}
		if (gs.selectedIndex < 0 && gs.options.length) gs.selectedIndex = 0;
	}
	renderTargets();
}

// renderTargets fills the target dropdown from lastTargets, filtered to the
// selected group for scenes.
function renderTargets() {
	const payload = lastTargets;
	if (!payload) return;
	let items = payload.items;
	if (isScene()) {
		const group = $("scene-group").value;
		items = items.filter((it) => it.group === group);
	}
	const select = $("target");
	select.innerHTML = "";
	const placeholder = document.createElement("option");
	placeholder.value = "";
	placeholder.textContent = "— choose a " + payload.kind + " —";
	select.appendChild(placeholder);
	for (const item of items) {
		const opt = document.createElement("option");
		opt.value = item.id;
		const state = isScene() ? (item.on ? "  (active)" : "") : (item.on ? "  (on)" : "  (off)");
		opt.textContent = item.name + state;
		opt.dataset.groupedLight = item.grouped_light || "";
		opt.dataset.group = item.group || "";
		opt.selected = item.id === settings.target;
		select.appendChild(opt);
	}
	select.disabled = false;
	setStatus(items.length + " " + payload.kind + (items.length === 1 ? "" : "s") + " found", "ok");
}

function onTargetChosen() {
	const select = $("target");
	const opt = select.options[select.selectedIndex];
	if (!opt || !opt.value) return;
	settings.bridge = $("bridge").value || settings.bridge || "";
	settings.kind = kindLabel().toLowerCase();
	settings.target = opt.value;
	settings.grouped_light = opt.dataset.groupedLight || undefined;
	settings.name = opt.textContent.replace(/\s+\((on|off|active)\)$/, "");
	if (isScene()) {
		const gs = $("scene-group");
		settings.group = gs.value;
		settings.group_name = (gs.options[gs.selectedIndex] || {}).textContent.replace(/\s+\((room|zone)\)$/, "");
		settings.dynamic = $("dynamic").checked || undefined;
	}
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

function onDynamicChanged() {
	settings.dynamic = $("dynamic").checked || undefined;
	if (!settings.target) return;
	saveSettings($("dynamic").checked ? "Saved: dynamic animation on" : "Saved: static recall");
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

// ---- Pairing wizard ----
// Shown while no bridge is paired. Discovery starts by itself; the user
// picks a bridge (or types an address), then is told when to press the
// button on the bridge. The plugin does the work and reports each stage.

let wizardStarted = false;
let discovered = []; // bridges from the last discovery

function showPairing(show) {
	$("pair").hidden = !show;
	$("configure").hidden = show;
	if (show && !wizardStarted) {
		wizardStarted = true;
		$("pair-button").addEventListener("click", onContinue);
		$("search-again").addEventListener("click", startDiscovery);
		$("retry-button").addEventListener("click", startDiscovery);
		$("pair-address").addEventListener("input", () => { $("pair-button").disabled = !$("pair-address").value.trim(); });
		startDiscovery();
	}
}

function setStep(n) {
	for (let i = 1; i <= 3; i++) {
		const li = $("step-" + i);
		li.className = i < n ? "done" : i === n ? "current" : "";
	}
	$("pair-find").hidden = n !== 1;
	$("pair-press").hidden = n !== 2;
	$("pair-result").hidden = n !== 3;
}

function startDiscovery() {
	setStep(1);
	$("bridge-list").innerHTML = "";
	$("manual-row").hidden = true;
	$("pair-button").disabled = true;
	setStatusOn("find-status", "Searching your network for a Hue bridge…", "busy");
	sendToPlugin({ event: "discover" });
}

function onBridgesFound(payload) {
	discovered = payload.items || [];
	const list = $("bridge-list");
	list.innerHTML = "";

	if (discovered.length === 0) {
		setStatusOn("find-status", payload.message
			? "No bridge found: " + payload.message
			: "No bridge found on your network. Enter its address below (the Hue app shows it under Settings > My Hue system).", "error");
	} else {
		setStatusOn("find-status", discovered.length === 1
			? "Found your bridge. Continue to pair with it."
			: "Found " + discovered.length + " bridges. Choose yours.", "ok");
	}

	discovered.forEach((b, i) => list.appendChild(choice("bridge", String(i),
		b.name || "Hue bridge", b.host + "  ·  " + b.id.slice(-6).toUpperCase(), i === 0)));
	list.appendChild(choice("manual", "manual", "Enter the address manually", "", discovered.length === 0));

	onChoiceChanged();
	for (const input of list.querySelectorAll("input")) input.addEventListener("change", onChoiceChanged);
}

// choice builds one radio row.
function choice(kind, value, name, detail, checked) {
	const label = document.createElement("label");
	label.className = "choice";
	const input = document.createElement("input");
	input.type = "radio";
	input.name = "bridge-choice";
	input.value = kind + ":" + value;
	input.checked = checked;
	label.appendChild(input);
	const n = document.createElement("span");
	n.className = "name";
	n.textContent = name;
	label.appendChild(n);
	if (detail) {
		const d = document.createElement("span");
		d.className = "detail";
		d.textContent = detail;
		label.appendChild(d);
	}
	return label;
}

function selectedChoice() {
	const input = document.querySelector('input[name="bridge-choice"]:checked');
	return input ? input.value : "";
}

function onChoiceChanged() {
	const manual = selectedChoice() === "manual:manual";
	$("manual-row").hidden = !manual;
	$("pair-button").disabled = manual ? !$("pair-address").value.trim() : !selectedChoice();
	if (manual) $("pair-address").focus();
}

function onContinue() {
	const sel = selectedChoice();
	let address = "", id = "";
	if (sel === "manual:manual") {
		address = $("pair-address").value.trim();
		if (!address) return;
	} else {
		const b = discovered[Number(sel.split(":")[1])];
		if (!b) return;
		address = b.host + ":" + (b.port || 443);
		id = b.id;
	}
	setStep(2);
	$("pair-countdown").hidden = true;
	$("press-instruction").textContent = "Connecting to the bridge…";
	setStatusOn("press-status", "", "busy");
	sendToPlugin({ event: "pair", address: address, id: id });
}

function onPairingProgress(payload) {
	switch (payload.stage) {
		case "searching":
		case "found":
			setStep(2);
			$("press-instruction").textContent = payload.message;
			break;
		case "waiting":
			setStep(2);
			$("press-instruction").textContent = "Press the round button on top of your Hue bridge now";
			$("pair-countdown").hidden = false;
			$("pair-countdown").textContent = payload.seconds_left + " s";
			setStatusOn("press-status", "Waiting for the button…", "busy");
			break;
		case "paired":
			setStep(3);
			setStatusOn("result-status", payload.message + " Loading your lights…", "ok");
			$("retry-button").hidden = true;
			setTimeout(requestTargets, 1200);
			break;
		case "error":
			setStep(3);
			$("step-3").textContent = "Not paired";
			setStatusOn("result-status", payload.message, "error");
			$("retry-button").hidden = false;
			break;
	}
}

function setStatusOn(id, text, state) {
	const el = $(id);
	el.textContent = text;
	el.className = "status" + (state ? " " + state : "");
}
