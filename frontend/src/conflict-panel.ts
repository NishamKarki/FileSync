// Author: Santosh Tripathee
// Conflict notification interface
// Shows the conflicts detected on this device, pops up a notice when a new
// one is detected, and lets the user choose Keep mine, Use incoming or
// Keep both. The Go side is in conflictScreen.go, conflictActions.go,
// conflictLog.go and conflictNotify.go.

import { GetConflicts, GetRegisteredDevices, ResolveConflict } from '../wailsjs/go/main/App';
import { main } from '../wailsjs/go/models';
import { EventsOn } from '../wailsjs/runtime/runtime';

import './conflict-panel.css';

// The three choices. The values match the constants in conflictScreen.go.
const conflictChoices = [
    { choice: 'keep-mine', label: 'Keep mine' },
    { choice: 'use-incoming', label: 'Use incoming' },
    { choice: 'keep-both', label: 'Keep both' },
];

let conflictsButton: HTMLButtonElement | null = null;
let conflictOverlay: HTMLDivElement | null = null;
let conflictNotification: HTMLDivElement | null = null;
let notificationTimer: number | undefined;

// Called once by main-window.ts, after the main window is on the screen
export function setupConflictPanel() {
    conflictsButton = document.querySelector<HTMLButtonElement>('#conflicts-button');
    conflictsButton?.addEventListener('click', () => { void openConflictPanel(); });

    // The panel, hidden until the Conflicts button is clicked
    conflictOverlay = document.createElement('div');
    conflictOverlay.className = 'conflict-overlay';
    conflictOverlay.hidden = true;
    conflictOverlay.innerHTML = `
        <div class="conflict-panel" role="dialog" aria-modal="true" aria-labelledby="conflict-panel-heading">
            <div class="conflict-panel-title">
                <h2 id="conflict-panel-heading">Synchronization Conflicts</h2>
                <button id="conflict-panel-close" type="button" aria-label="Close">×</button>
            </div>
            <p id="conflict-panel-status" role="status"></p>
            <div id="conflict-list"></div>
        </div>`;
    document.body.appendChild(conflictOverlay);

    // The pop-up notice, hidden until a conflict is detected
    conflictNotification = document.createElement('div');
    conflictNotification.className = 'conflict-notification';
    conflictNotification.setAttribute('role', 'alert');
    conflictNotification.hidden = true;
    conflictNotification.innerHTML = `
        <p id="conflict-notification-text"></p>
        <div>
            <button id="conflict-notification-review" type="button">Review conflict</button>
            <button id="conflict-notification-dismiss" type="button">Dismiss</button>
        </div>`;
    document.body.appendChild(conflictNotification);

    document.getElementById('conflict-panel-close')?.addEventListener('click', closeConflictPanel);
    document.getElementById('conflict-notification-review')?.addEventListener('click', () => {
        hideConflictNotification();
        void openConflictPanel();
    });
    document.getElementById('conflict-notification-dismiss')?.addEventListener('click', hideConflictNotification);

    // Clicking the dark background or pressing Escape closes the panel
    conflictOverlay.addEventListener('click', (clickEvent) => {
        if (clickEvent.target === conflictOverlay) closeConflictPanel();
    });
    document.addEventListener('keydown', (keyEvent) => {
        if (keyEvent.key === 'Escape' && conflictOverlay && !conflictOverlay.hidden) closeConflictPanel();
    });

    // Sent by conflictNotify.go the moment a conflict is detected
    EventsOn('conflict-detected', (fileName: string) => {
        showConflictNotification(fileName);
        void refreshConflictCount();
        if (conflictOverlay && !conflictOverlay.hidden) void renderConflictList();
    });

    // Show the count for conflicts saved before the app was started
    void refreshConflictCount();
}

async function openConflictPanel() {
    if (!conflictOverlay) return;
    conflictOverlay.hidden = false;
    document.getElementById('conflict-panel-close')?.focus();
    await renderConflictList();
}

function closeConflictPanel() {
    if (!conflictOverlay) return;
    conflictOverlay.hidden = true;
    conflictsButton?.focus();
}

// Updates the button label, for example "Conflicts (2)"
function updateConflictsButton(openCount: number) {
    if (!conflictsButton) return;
    conflictsButton.textContent = openCount > 0 ? `Conflicts (${openCount})` : 'Conflicts';
    conflictsButton.classList.toggle('has-conflicts', openCount > 0);
}

async function refreshConflictCount() {
    try {
        const openConflicts = await GetConflicts();
        updateConflictsButton(openConflicts.length);
    } catch (conflictCountError) {
        if (conflictsButton) conflictsButton.title = `Cannot load conflicts: ${conflictCountError}`;
    }
}

function showConflictNotification(fileName: string) {
    if (!conflictNotification) return;
    const notificationText = document.getElementById('conflict-notification-text');
    if (notificationText) {
        notificationText.textContent = `Conflict detected for ${fileName}. It was changed on this device and on another device before they synced.`;
    }
    conflictNotification.hidden = false;

    // Hide it again after 15 seconds; the Conflicts button still shows the count
    window.clearTimeout(notificationTimer);
    notificationTimer = window.setTimeout(hideConflictNotification, 15000);
}

function hideConflictNotification() {
    if (conflictNotification) conflictNotification.hidden = true;
}

// Shows a message at the top of the panel, in red if it is an error
function setPanelStatus(statusMessage: string, isError: boolean) {
    const panelStatus = document.getElementById('conflict-panel-status');
    if (!panelStatus) return;
    panelStatus.textContent = statusMessage;
    panelStatus.classList.toggle('conflict-status-error', isError);
}

// Loads the open conflicts from Go and draws one card for each
async function renderConflictList() {
    const conflictList = document.getElementById('conflict-list');
    if (!conflictList) return;

    let openConflicts: main.ConflictRecord[];
    try {
        openConflicts = await GetConflicts();
    } catch (conflictLoadingError) {
        conflictList.textContent = `Cannot load conflicts: ${conflictLoadingError}`;
        return;
    }
    updateConflictsButton(openConflicts.length);

    const deviceNames = await loadDeviceNames();
    conflictList.replaceChildren();
    if (openConflicts.length === 0) {
        conflictList.appendChild(createTextElement('p', 'conflict-empty', 'No conflicts are waiting for a decision.'));
        return;
    }
    for (const conflict of openConflicts) {
        conflictList.appendChild(buildConflictCard(conflict, deviceNames));
    }
}

// Device IDs mapped to device names, from Rabindra's registered device list.
// If the coordinator doesn't answer within 1.5 seconds, the panel shows a
// shortened device ID instead of the name.
async function loadDeviceNames(): Promise<Map<string, string>> {
    const deviceNames = new Map<string, string>();
    try {
        const coordinatorTimeout = new Promise<never>((_, reject) => {
            window.setTimeout(() => reject(new Error('timed out')), 1500);
        });
        const registeredDevices = await Promise.race([GetRegisteredDevices(), coordinatorTimeout]);
        for (const device of registeredDevices) {
            deviceNames.set(device.id, device.name);
        }
    } catch {
        // Names are optional
    }
    return deviceNames;
}

function describeDevice(deviceID: string, deviceNames: Map<string, string>): string {
    if (!deviceID) return 'another device';
    return deviceNames.get(deviceID) ?? `device ${deviceID.slice(0, 8)}`;
}

// Converts the UTC time saved in the database to the user's local time
function formatDetectedTime(detectedAt: string): string {
    const detectedTime = new Date(detectedAt);
    return Number.isNaN(detectedTime.getTime()) ? detectedAt : detectedTime.toLocaleString();
}

// File names come from other devices, so they are always inserted as text
// (textContent), never as HTML
function createTextElement(tagName: string, className: string, text: string): HTMLElement {
    const element = document.createElement(tagName);
    if (className) element.className = className;
    element.textContent = text;
    return element;
}

function buildVersionCard(title: string, device: string, contentHash: string): HTMLElement {
    const versionCard = document.createElement('div');
    versionCard.className = 'conflict-version';
    versionCard.append(
        createTextElement('p', 'conflict-version-title', title),
        createTextElement('p', 'conflict-version-device', device),
        // The first 8 characters of the SHA-256 are enough to tell versions apart
        createTextElement('p', 'conflict-version-content', `Content ID: ${contentHash.slice(0, 8)}`),
    );
    return versionCard;
}

function buildConflictCard(conflict: main.ConflictRecord, deviceNames: Map<string, string>): HTMLElement {
    const conflictCard = document.createElement('section');
    conflictCard.className = 'conflict-card';

    const versionCards = document.createElement('div');
    versionCards.className = 'conflict-versions';
    versionCards.append(
        buildVersionCard('Your version', 'This device', conflict.localHash),
        buildVersionCard('Incoming version', `From ${describeDevice(conflict.senderDeviceId, deviceNames)}`, conflict.incomingHash),
    );

    const choiceButtons = document.createElement('div');
    choiceButtons.className = 'conflict-actions';
    for (const conflictChoice of conflictChoices) {
        const choiceButton = document.createElement('button');
        choiceButton.type = 'button';
        choiceButton.textContent = conflictChoice.label;
        choiceButton.addEventListener('click', () => {
            void resolveConflict(conflict, conflictChoice.choice, choiceButtons);
        });
        choiceButtons.appendChild(choiceButton);
    }

    conflictCard.append(
        createTextElement('p', 'conflict-label', 'Conflicting file'),
        createTextElement('h3', '', conflict.fileId),
        createTextElement('p', 'conflict-notice', 'This file was changed on this device and on another device before they synced. FileSync kept both versions. Choose which one to keep.'),
        versionCards,
        createTextElement('p', 'conflict-detected-time', `Detected ${formatDetectedTime(conflict.detectedAt)}`),
        choiceButtons,
        createTextElement('p', 'conflict-choice-help', 'Nothing is deleted: Keep mine archives their version, Use incoming backs up yours first, and Keep both adds theirs as a separate file.'),
    );
    return conflictCard;
}

// Runs when a choice button is clicked
async function resolveConflict(conflict: main.ConflictRecord, choice: string, choiceButtons: HTMLElement) {
    // Prevents a double click while Go is working
    choiceButtons.querySelectorAll('button').forEach(choiceButton => { choiceButton.disabled = true; });
    try {
        const resolution = await ResolveConflict(conflict.id, choice);
        setPanelStatus(resolution.message, false);
        await renderConflictList();
    } catch (conflictResolvingError) {
        setPanelStatus(`Could not resolve ${conflict.fileId}: ${conflictResolvingError}`, true);
        choiceButtons.querySelectorAll('button').forEach(choiceButton => { choiceButton.disabled = false; });
    }
}