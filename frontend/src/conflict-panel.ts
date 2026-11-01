// Author: Santosh Tripathee
// Conflict panel for FileSync.
// Shows the conflicts found on this device, pops up a notice when a new one
// comes in, and lets the user pick Keep mine, Use incoming or Keep both.
// The Go side is in conflictScreen.go, conflictActions.go, conflictLog.go
// and conflictNotify.go.

import { GetConflicts, GetRegisteredDevices, ResolveConflict } from '../wailsjs/go/main/App';
import { main } from '../wailsjs/go/models';
import { EventsOn } from '../wailsjs/runtime/runtime';

import './conflict-panel.css';

// These have to match the constants in conflictScreen.go,
// otherwise Go won't know which choice was clicked
const conflictChoices = [
    { choice: 'keep-mine', label: 'Keep mine' },
    { choice: 'use-incoming', label: 'Use incoming' },
    { choice: 'keep-both', label: 'Keep both' },
];

// Set up in setupConflictPanel below
let conflictsButton: HTMLButtonElement | null = null;
let conflictOverlay: HTMLDivElement | null = null;
let conflictNotification: HTMLDivElement | null = null;
let notificationTimer: number | undefined;

// main-window.ts calls this once, after the main window is on the screen
export function setupConflictPanel() {
    conflictsButton = document.querySelector<HTMLButtonElement>('#conflicts-button');
    conflictsButton?.addEventListener('click', () => { void openConflictPanel(); });

    // The panel itself, hidden until someone clicks the Conflicts button
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

    // Small notice in the corner, hidden until a conflict comes in
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

    // Close, Review conflict and Dismiss buttons
    document.getElementById('conflict-panel-close')?.addEventListener('click', closeConflictPanel);
    document.getElementById('conflict-notification-review')?.addEventListener('click', () => {
        hideConflictNotification();
        void openConflictPanel();
    });
    document.getElementById('conflict-notification-dismiss')?.addEventListener('click', hideConflictNotification);

    // Clicking outside the panel or pressing Escape closes it
    conflictOverlay.addEventListener('click', (clickEvent) => {
        if (clickEvent.target === conflictOverlay) closeConflictPanel();
    });
    document.addEventListener('keydown', (keyEvent) => {
        if (keyEvent.key === 'Escape' && conflictOverlay && !conflictOverlay.hidden) closeConflictPanel();
    });

    // conflictNotify.go sends this as soon as a conflict is detected
    EventsOn('conflict-detected', (fileName: string) => {
        showConflictNotification(fileName);
        void refreshConflictCount();
        // If the panel is already open, redraw it so the new conflict shows up
        if (conflictOverlay && !conflictOverlay.hidden) void renderConflictList();
    });

    // Conflicts saved before the app was started should still show in the count
    void refreshConflictCount();
}

async function openConflictPanel() {
    if (!conflictOverlay) return;
    conflictOverlay.hidden = false;
    // Focus the X button so keyboard users start inside the panel
    document.getElementById('conflict-panel-close')?.focus();
    await renderConflictList();
}

function closeConflictPanel() {
    if (!conflictOverlay) return;
    conflictOverlay.hidden = true;
    // Give focus back to the Conflicts button
    conflictsButton?.focus();
}

// Puts the count on the button, like "Conflicts (2)", and turns it orange
// while anything is waiting
function updateConflictsButton(openCount: number) {
    if (!conflictsButton) return;
    conflictsButton.textContent = openCount > 0 ? `Conflicts (${openCount})` : 'Conflicts';
    conflictsButton.classList.toggle('has-conflicts', openCount > 0);
}

// Only updates the number on the button, the panel stays closed
async function refreshConflictCount() {
    try {
        const openConflicts = await GetConflicts();
        updateConflictsButton(openConflicts.length);
    } catch (conflictCountError) {
        // Show the error as a tooltip on the button instead of breaking the window
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

    // Goes away by itself after 15 seconds, the count stays on the button anyway.
    // Clear the old timer first so a second conflict doesn't get cut short.
    window.clearTimeout(notificationTimer);
    notificationTimer = window.setTimeout(hideConflictNotification, 15000);
}

function hideConflictNotification() {
    if (conflictNotification) conflictNotification.hidden = true;
}

// Message at the top of the panel, red when something went wrong
function setPanelStatus(statusMessage: string, isError: boolean) {
    const panelStatus = document.getElementById('conflict-panel-status');
    if (!panelStatus) return;
    panelStatus.textContent = statusMessage;
    panelStatus.classList.toggle('conflict-status-error', isError);
}

// Gets the open conflicts from Go and builds a card for each one
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
    // Clear the old cards before drawing them again
    conflictList.replaceChildren();
    if (openConflicts.length === 0) {
        conflictList.appendChild(createTextElement('p', 'conflict-empty', 'No conflicts are waiting for a decision.'));
        return;
    }
    for (const conflict of openConflicts) {
        conflictList.appendChild(buildConflictCard(conflict, deviceNames));
    }
}

// Matches device IDs to device names using Rabindra's registered device list.
// If the coordinator takes more than 1.5 seconds we stop waiting and just show
// a short ID, so the panel never gets stuck loading.
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
        // Not a problem, the panel just shows IDs instead of names
    }
    return deviceNames;
}

// Device name if we have it, otherwise the first 8 characters of the ID
function describeDevice(deviceID: string, deviceNames: Map<string, string>): string {
    if (!deviceID) return 'another device';
    return deviceNames.get(deviceID) ?? `device ${deviceID.slice(0, 8)}`;
}

// The database saves the time in UTC, so show it in the user's local time.
// If the date can't be read, just show it the way it was saved.
function formatDetectedTime(detectedAt: string): string {
    const detectedTime = new Date(detectedAt);
    return Number.isNaN(detectedTime.getTime()) ? detectedAt : detectedTime.toLocaleString();
}

// File names come from other devices, so they always go in as plain text
// (textContent) and never as HTML
function createTextElement(tagName: string, className: string, text: string): HTMLElement {
    const element = document.createElement(tagName);
    if (className) element.className = className;
    element.textContent = text;
    return element;
}

// One of the two boxes, "Your version" or "Incoming version"
function buildVersionCard(title: string, device: string, contentHash: string): HTMLElement {
    const versionCard = document.createElement('div');
    versionCard.className = 'conflict-version';
    versionCard.append(
        createTextElement('p', 'conflict-version-title', title),
        createTextElement('p', 'conflict-version-device', device),
        // First 8 characters of the hash are enough to tell the two versions apart
        createTextElement('p', 'conflict-version-content', `Content ID: ${contentHash.slice(0, 8)}`),
    );
    return versionCard;
}

// Builds the card for one conflict: file name, both versions and the three buttons
function buildConflictCard(conflict: main.ConflictRecord, deviceNames: Map<string, string>): HTMLElement {
    const conflictCard = document.createElement('section');
    conflictCard.className = 'conflict-card';

    const versionCards = document.createElement('div');
    versionCards.className = 'conflict-versions';
    versionCards.append(
        buildVersionCard('Your version', 'This device', conflict.localHash),
        buildVersionCard('Incoming version', `From ${describeDevice(conflict.senderDeviceId, deviceNames)}`, conflict.incomingHash),
    );

    // One button for each choice
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

// Runs when one of the three buttons is clicked
async function resolveConflict(conflict: main.ConflictRecord, choice: string, choiceButtons: HTMLElement) {
    // Turn the buttons off while Go is working, so a double click can't run it twice
    choiceButtons.querySelectorAll('button').forEach(choiceButton => { choiceButton.disabled = true; });
    try {
        const resolution = await ResolveConflict(conflict.id, choice);
        setPanelStatus(resolution.message, false);
        // Redraw the list so the resolved conflict goes away
        await renderConflictList();
    } catch (conflictResolvingError) {
        setPanelStatus(`Could not resolve ${conflict.fileId}: ${conflictResolvingError}`, true);
        // Something failed, so turn the buttons back on and let the user try again
        choiceButtons.querySelectorAll('button').forEach(choiceButton => { choiceButton.disabled = false; });
    }
}