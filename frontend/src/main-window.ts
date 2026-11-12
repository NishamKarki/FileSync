// Author: Rabindra Neupane
// Main FileSync window: file list, devices, recent activity and the sync buttons.
// Nisham, Rabindra and Santosh all have parts in here, marked by name below.

// Go functions from app.go that Wails makes available to the frontend
import {
    AddFiles,
    ListSyncedFiles,
    DiscoverDevices,
    GetLocalIP,
    SendFile,
    SyncFile,
    ConfigureCoordinator,
    GetRegistrationStatus,
    GetRegisteredDevices,
    StartCoordinatorPairing,
    GetCoordinatorPairingStatus,
    RemoveRegisteredDevice
} from '../wailsjs/go/main/App';

// EventsOn listens for events sent from Go, like the file changes from fileWatcher.go
import { EventsOn } from '../wailsjs/runtime/runtime';

import './main-window.css'; // styles for this window

// Santosh Tripathee - conflict notification interface
import { setupConflictPanel } from './conflict-panel' ;

// Layout of the whole window. The functions below fill in the lists and status text.
document.querySelector<HTMLDivElement>('#app')!.innerHTML = `
    <div class="app">

        <header class="title">
            <h1>FileSync</h1>
        </header>

        <section class="select-file-to-sync">
            <div>
                <h2>Choose file to sync</h2>
                <p class="file-path" id="selected-files">No file selected</p>
            </div>

            <div>
                <button id="select-file-button">Add files from computer</button>
                <p>Add files to Synced Files.</p>
            </div>
        </section>

        <section class="file-details">
            <h2>Files in Synced Files</h2>
            <p>Select one or more files below to sync their working copies.</p>
            <p id="file-list-status" role="status"></p>

            <div class="file-table-scroll" tabindex="0" role="region" aria-label="Files in Synced Files, scroll to view more">
            <table class="file-table">
                <thead>
                    <tr>
                        <th>Name</th>
                        <th>Status</th>
                        <th>Size</th>
                        <th>Modified</th>
                    </tr>
                </thead>

                <tbody id="synced-file-list"></tbody>
            </table>
            </div>
        </section>

        <section class="progress-activity">

            <div class="section devices">
                <h2>Connected Devices</h2>
                <div class="coordinator-controls">
                    <fieldset class="role-options">
                        <legend>Choose this computer's role</legend>
                        <label><input type="radio" id="coordinator-mode" name="device-role"> Coordinator</label>
                        <label><input type="radio" id="client-mode" name="device-role" checked> Client</label>
                    </fieldset>
                    <div id="client-connection-controls">
                        <label for="coordinator-address">Coordinator IP:port</label>
                        <input id="coordinator-address" placeholder="192.168.1.10:8080" autocomplete="off">
                        <label id="pairing-code-label" for="pairing-code">Pairing code</label>
                        <input id="pairing-code" maxlength="6" inputmode="numeric" placeholder="6-digit code" autocomplete="one-time-code">
                        <p class="connection-guidance">Ask the coordinator to allow a new device, then enter its code.</p>
                        <button id="find-coordinator-button">Find Coordinator</button>
                    </div>
                    <div id="coordinator-pairing-controls" hidden>
                        <p>This computer accepts new clients only while pairing is open.</p>
                        <button id="start-pairing-button">Allow New Device for 2 Minutes</button>
                        <div id="pairing-status" aria-live="polite">Pairing is closed.</div>
                    </div>
                    <button id="save-coordinator-button">Connect</button>
                    <div id="registration-status" aria-live="polite">Loading device registration...</div>
                    <div id="connection-help" aria-live="polite"></div>
                </div>
                <button id="registered-devices-button">Registered Devices</button>

                <div id="connected-devices">
                    <p>Registered devices will appear after coordinator setup.</p>
                </div>
            </div>

            <div class="section activity">
                <h2>Recent Activity</h2>

                <p id="recent-activity">No Files Modified</p>
            </div>

        </section>

        <footer class="footer">
            <button id="sync-now-button">Sync Now</button>
            <details class="transfer-diagnostics">
                <summary>Transfer diagnostics</summary>
                <div>
                    <button id="send-whole-file-button">Send Whole File</button>
                </div>
            </details>

            <div>
                <button id="conflicts-button">Conflicts</button>
                <button>Settings</button>
            </div>
        </footer>

    </div>
`
// Rabindra Neupane
// Prints this computer's local IP in the console. Only used while developing.
async function testLocalIP() {
    try {
        const localIP = await GetLocalIP();
        console.log("FileSync local IP address:");
        console.log(localIP);
    }
    catch (error) {
        console.error("FileSync failed to get local IP address", error);
    }
}
testLocalIP();

// Rabindra Neupane
// Scans the network for a coordinator. Only runs when the user clicks Find Coordinator.
async function findCoordinator() {
    // Don't start a second scan while one is already running
    if (coordinatorSearchRunning) return;
    coordinatorSearchRunning = true;
    showingDiscovery = true;
    selectedDeviceAddress = null;
    const container = document.getElementById('connected-devices');
    if (container) container.textContent = 'Searching for coordinators...';
    const button = document.querySelector<HTMLButtonElement>('#find-coordinator-button');
    if (button) button.disabled = true;
    try {
        const devices = await DiscoverDevices();
        // Only list the devices that are coordinators
        if (showingDiscovery) renderDevices((devices || []).filter(device => device.coordinator));
    } catch (error) {
        if (showingDiscovery && container) container.textContent = `Coordinator search failed: ${error}`;
    } finally {
        coordinatorSearchRunning = false;
        if (button) button.disabled = false;
    }
}
// Rabindra Neupane
document.getElementById('find-coordinator-button')?.addEventListener('click', () => { void findCoordinator(); });
// Which device is picked for syncing, plus a few flags so scans and refreshes don't overlap
let selectedDeviceAddress: string | null = null;
let selectedDeviceID: string | null = null;
let showingDiscovery = false;
let coordinatorSearchRunning = false;
let registrationRefreshRunning = false;
let registrationSettingsLoaded = false;
let registrationSettingsEdited = false;
let ownDeviceID = '';
let ownDeviceName = '';
let ownDeviceAddress = '';
let ownDeviceIsCoordinator = false;
let pairingExpiresAt = 0;
let activePairingCode = '';

// Rabindra Neupane
// A device as it comes back from the coordinator
type ListedDevice = { id: string; name: string; ip: string; port: number; online: boolean; lastSeen?: string; coordinator?: boolean };

// Rabindra Neupane
// formatLastSeen converts the coordinator timestamp to this computer's timezone.
function formatLastSeen(lastSeen: string): string {
    // Parse the UTC timestamp received from the coordinator.
    const lastSeenDate = new Date(lastSeen);

    // Keep the original value when the timestamp cannot be parsed.
    if (Number.isNaN(lastSeenDate.getTime())) return lastSeen;

    // Format the time using the current device's language and timezone settings.
    return new Intl.DateTimeFormat(undefined, {
        year: 'numeric',
        month: 'short',
        day: 'numeric',
        hour: 'numeric',
        minute: '2-digit',
        second: '2-digit',
        timeZoneName: 'short'
    }).format(lastSeenDate);
}

// Rabindra Neupane
// renderDevices lists online devices first and keeps offline records visible.
function renderDevices(devices: ListedDevice[]) {
    const container = document.getElementById('connected-devices');
    if (!container) return;
    const previousAddress = selectedDeviceAddress;
    selectedDeviceAddress = null;
    container.replaceChildren();
    // Leave this computer out of its own list and put online devices first
    const peers = devices
        .filter(device => !ownDeviceID || device.id !== ownDeviceID)
        .sort((a, b) => Number(b.online) - Number(a.online));
    if (peers.length === 0) {
        container.textContent = showingDiscovery ? 'No coordinator found. Start one on another computer or enter its address manually.' : 'No other registered devices yet.';
        return;
    }
    for (const device of peers) {
        // IPv6 addresses need square brackets before the port
        const address = device.ip.includes(':') ? `[${device.ip}]:${device.port}` : `${device.ip}:${device.port}`;
        const row = document.createElement('button');
        row.type = 'button';
        row.className = 'device';
        row.disabled = !device.online;
        // Show the last-seen value in the timezone of the computer viewing the list.
        if (device.lastSeen) row.title = `Last seen: ${formatLastSeen(device.lastSeen)}\nDevice ID: ${device.id}`;
        // Discovery chooses setup details; only registered rows choose sync targets.
        if (showingDiscovery) {
            row.textContent = `${device.name} - Coordinator - ${address} - Use this coordinator`;
            row.addEventListener('click', () => {
                const mode = document.querySelector<HTMLInputElement>('#coordinator-mode');
                const clientMode = document.querySelector<HTMLInputElement>('#client-mode');
                const input = document.querySelector<HTMLInputElement>('#coordinator-address');
                if (!mode || !input) return;
                mode.checked = false;
                if (clientMode) clientMode.checked = true;
                input.disabled = false;
                input.value = address;
                registrationSettingsEdited = true;
                updateCoordinatorSecurityControls(false);
                const label = document.getElementById('registration-status');
                if (label) label.textContent = 'Coordinator selected. Enter its pairing code and click Connect.';
            });
            container.appendChild(row);
            continue;
        }
        appendDetailRows(row, [
            ['Device name', device.name || 'Unknown device'],
            ['Device ID', device.id || 'Not assigned'],
            ['Address', address],
            ['Status', device.online ? 'Online' : 'Offline'],
            ['Role', device.coordinator ? 'Coordinator' : 'Client']
        ]);
        // Keep the same device selected after the list refreshes
        const selected = selectedDeviceID ? device.id === selectedDeviceID : address === previousAddress;
        if (selected && device.online) {
            selectedDeviceAddress = address;
            row.classList.add('selected-device');
        }
        row.addEventListener('click', () => {
            if (!device.online) return;
            selectedDeviceID = device.id || null;
            selectedDeviceAddress = address;
            container.querySelectorAll('.device').forEach(element => element.classList.remove('selected-device'));
            row.classList.add('selected-device');
        });
        // Group the selectable device card with its coordinator-only revoke action.
        const deviceEntry = document.createElement('div');
        deviceEntry.className = 'device-entry';
        deviceEntry.appendChild(row);
        if (ownDeviceIsCoordinator && !device.coordinator) {
            const removeButton = document.createElement('button');
            removeButton.type = 'button';
            removeButton.className = 'remove-device-button';
            removeButton.textContent = 'Remove device';
            removeButton.addEventListener('click', async () => {
                // Confirm the destructive credential revocation with the coordinator user.
                if (!window.confirm(`Remove ${device.name || 'this device'} from the coordinator?`)) return;
                removeButton.disabled = true;
                try {
                    await RemoveRegisteredDevice(device.id);
                    if (selectedDeviceID === device.id) {
                        selectedDeviceID = null;
                        selectedDeviceAddress = null;
                    }
                    await refreshRegistration();
                } catch (error) {
                    window.alert(`Cannot remove device: ${error}`);
                    removeButton.disabled = false;
                }
            });
            deviceEntry.appendChild(removeButton);
        }
        container.appendChild(deviceEntry);
    }
}

// Rabindra Neupane
// Fills a box with "label: value" rows. Everything goes in as text, not HTML,
// since device names come from other computers.
function appendDetailRows(container: HTMLElement, details: string[][]) {
    // Clear out the old rows first
    container.replaceChildren();

    for (const [detailLabel, detailValue] of details) {
        const detailRow = document.createElement('div');
        detailRow.className = 'registration-detail-row';

        const labelElement = document.createElement('span');
        labelElement.className = 'registration-detail-label';
        labelElement.textContent = `${detailLabel}:`;

        const valueElement = document.createElement('span');
        valueElement.className = 'registration-detail-value';
        valueElement.textContent = detailValue;

        detailRow.append(labelElement, valueElement);
        container.appendChild(detailRow);
    }
}

// Rabindra Neupane
// friendlyConnectionError turns technical network failures into useful instructions.
function friendlyConnectionError(error: unknown): string {
    // Match common coordinator and authentication failures without exposing raw URLs.
    const message = String(error ?? '').toLowerCase();
    if (message.includes('deadline exceeded') || message.includes('timed out')) {
        return 'Coordinator did not respond. Confirm FileSync is open and both computers are on the same network.';
    }
    if (message.includes('connection refused')) {
        return 'The computer was found, but FileSync is not accepting connections on port 8080.';
    }
    if (message.includes('pairing is closed')) {
        return 'Pairing is closed. Ask the coordinator to click Allow New Device.';
    }
    if (message.includes('pairing code is invalid')) {
        return 'The pairing code is invalid or expired. Generate a new code on the coordinator.';
    }
    if (message.includes('too many pairing attempts')) {
        return 'Too many incorrect pairing attempts. Wait five minutes before trying again.';
    }
    if (message.includes('authentication') || message.includes('not registered')) {
        return 'This device is not authenticated. Pair it with the coordinator again.';
    }
    if (message.includes('unavailable')) {
        return 'Coordinator unavailable. Check its address, firewall permission, and network connection.';
    }
    return String(error ?? 'Connection failed.');
}

// Rabindra Neupane
// Shows this computer's role, the coordinator address and whether it's registered
function renderRegistrationStatus(
    container: HTMLElement,
    status: Awaited<ReturnType<typeof GetRegistrationStatus>>
) {
    // Nothing set up yet, so just tell the user what to do
    if (!status.configured) {
        container.classList.remove('registration-details');
        container.textContent = 'Choose this computer as coordinator, or enter the coordinator address.';
        return;
    }

    const registrationState = status.registered
        ? 'Securely connected'
        : status.lastError ? friendlyConnectionError(status.lastError) : 'Connecting';
    const registrationDetails = [
        ['Role', status.coordinator ? 'Coordinator' : 'Client'],
        ['Device name', status.deviceName || 'Unknown device'],
        ['Coordinator', status.coordinatorAddress || 'Connecting'],
        ['Status', registrationState],
        ['Device ID', status.deviceId || 'Not assigned']
    ];

    container.classList.add('registration-details');
    appendDetailRows(container, registrationDetails);

    // Keep the full technical error available without placing it in the main status table.
    const connectionHelp = document.getElementById('connection-help');
    if (connectionHelp) {
        connectionHelp.replaceChildren();
        if (!status.registered && status.lastError) {
            const retryMessage = document.createElement('p');
            retryMessage.textContent = 'Registration retries automatically every five seconds.';
            const details = document.createElement('details');
            const summary = document.createElement('summary');
            summary.textContent = 'Technical details';
            const technicalError = document.createElement('pre');
            technicalError.textContent = status.lastError;
            details.append(summary, technicalError);
            connectionHelp.append(retryMessage, details);
        }
    }
}

// Rabindra Neupane
// Gets the registration status and the device list from the coordinator.
// Skips if the last refresh hasn't finished yet.
async function refreshRegistration() {
    if (registrationRefreshRunning) return;
    registrationRefreshRunning = true;
    const label = document.getElementById('registration-status');
    try {
        const status = await GetRegistrationStatus();
        ownDeviceID = status.deviceId;
        ownDeviceName = status.deviceName;
        ownDeviceAddress = status.localAddress;
        ownDeviceIsCoordinator = status.coordinator;
        const mode = document.querySelector<HTMLInputElement>('#coordinator-mode');
        const clientMode = document.querySelector<HTMLInputElement>('#client-mode');
        const address = document.querySelector<HTMLInputElement>('#coordinator-address');
        // Fill in the saved settings once at startup, but never over something the user is typing
        if (!registrationSettingsLoaded && !registrationSettingsEdited && mode && address) {
            mode.checked = status.coordinator;
            if (clientMode) clientMode.checked = !status.coordinator;
            address.value = status.coordinatorAddress;
            address.disabled = mode.checked;
        }
        // Keep unsaved controls aligned with the checkbox instead of the old backend role.
        const displayedCoordinatorMode = registrationSettingsEdited
            ? (mode?.checked ?? status.coordinator)
            : status.coordinator;
        updateCoordinatorSecurityControls(displayedCoordinatorMode);
        registrationSettingsLoaded = true;
        if (label) {
            renderRegistrationStatus(label, status);
            // Keep the selected draft role visible until the user applies it.
            showPendingRole(displayedCoordinatorMode);
        }
        if (!status.configured && !showingDiscovery) renderDevices([]);
        if (status.configured && !showingDiscovery) {
            const devices = await GetRegisteredDevices();
            if (!showingDiscovery) renderDevices(devices);
        }
        // Clear a one-time client code after successful registration.
        if (status.registered && !status.coordinator) {
            const pairingCode = document.querySelector<HTMLInputElement>('#pairing-code');
            if (pairingCode) pairingCode.value = '';
        }
        // Refresh the temporary pairing code only on the coordinator.
        if (status.coordinator) await refreshPairingStatus();
    } catch (error) {
        if (label) label.textContent = friendlyConnectionError(error);
        showConnectionHelp(error);
        if (!showingDiscovery) {
            selectedDeviceAddress = null;
            const container = document.getElementById('connected-devices');
            if (container) container.textContent = 'Coordinator unavailable. Registration will retry automatically.';
        }
    } finally {
        registrationRefreshRunning = false;
    }
}

//Rabindra Neupane
// Coordinator settings: the checkbox, the address box and the Apply button
const coordinatorMode = document.querySelector<HTMLInputElement>('#coordinator-mode');
const clientMode = document.querySelector<HTMLInputElement>('#client-mode');
const coordinatorAddress = document.querySelector<HTMLInputElement>('#coordinator-address');
const pairingCodeInput = document.querySelector<HTMLInputElement>('#pairing-code');
const startPairingButton = document.querySelector<HTMLButtonElement>('#start-pairing-button');
const saveCoordinatorButton = document.querySelector<HTMLButtonElement>('#save-coordinator-button');

// Rabindra Neupane
// showConnectionHelp keeps raw network details available without crowding the main message.
function showConnectionHelp(error: unknown) {
    // Create an expandable technical message for troubleshooting.
    const connectionHelp = document.getElementById('connection-help');
    if (!connectionHelp) return;
    connectionHelp.replaceChildren();
    const details = document.createElement('details');
    const summary = document.createElement('summary');
    summary.textContent = 'Technical details';
    const technicalError = document.createElement('pre');
    technicalError.textContent = String(error ?? 'No additional details.');
    details.append(summary, technicalError);
    connectionHelp.append(details);
}

// Rabindra Neupane
// showPendingRole updates the visible role before the new setting is applied.
function showPendingRole(coordinator: boolean) {
    // Leave the saved backend status unchanged until the user selects another role.
    if (!registrationSettingsEdited) return;
    const statusContainer = document.getElementById('registration-status');
    if (!statusContainer) return;

    // Show the selected role and explain the button needed to activate it.
    const selectedRole = coordinator ? 'Coordinator' : 'Client';
    const pendingStatus = coordinator
        ? 'Not started — click Start Coordinator'
        : 'Not connected — enter the pairing code and click Connect';
    statusContainer.classList.add('registration-details');
    appendDetailRows(statusContainer, [
        ['Role', selectedRole],
        ['Device name', ownDeviceName || 'Unknown device'],
        ['Coordinator', coordinator ? (ownDeviceAddress || 'Not started') : (coordinatorAddress?.value.trim() || 'Not selected')],
        ['Status', pendingStatus],
        ['Device ID', ownDeviceID || 'Not assigned']
    ]);
}

// Rabindra Neupane
// updateCoordinatorSecurityControls switches between coordinator and client pairing controls.
function updateCoordinatorSecurityControls(coordinator: boolean) {
    // Show only the controls that belong to the selected role.
    const clientControls = document.getElementById('client-connection-controls');
    const coordinatorControls = document.getElementById('coordinator-pairing-controls');
    if (clientControls) clientControls.hidden = coordinator;
    // Pairing is available only after this device is running as coordinator.
    if (coordinatorControls) coordinatorControls.hidden = !coordinator || !ownDeviceIsCoordinator || registrationSettingsEdited;
    if (coordinatorMode) coordinatorMode.checked = coordinator;
    if (clientMode) clientMode.checked = !coordinator;
    if (coordinatorAddress) coordinatorAddress.disabled = coordinator;
    if (saveCoordinatorButton) {
        saveCoordinatorButton.textContent = coordinator ? 'Start Coordinator' : 'Connect';
        // Put Start Coordinator before pairing, but keep Connect after client settings.
        if (coordinator && coordinatorControls) coordinatorControls.before(saveCoordinatorButton);
        if (!coordinator && clientControls) clientControls.after(saveCoordinatorButton);
    }
    showPendingRole(coordinator);
}

// Rabindra Neupane
// renderPairingCountdown shows how long the temporary code can still be used.
function renderPairingCountdown() {
    // Stop when the pairing message is not on this page.
    const pairingStatus = document.getElementById('pairing-status');
    if (!pairingStatus || !activePairingCode || pairingExpiresAt <= 0) return;

    // Calculate a readable minutes-and-seconds countdown.
    const remainingSeconds = Math.max(0, Math.ceil((pairingExpiresAt - Date.now()) / 1000));
    if (remainingSeconds === 0) {
        pairingStatus.textContent = 'Pairing code expired. Click Allow New Device to create another code.';
        activePairingCode = '';
        pairingExpiresAt = 0;
        return;
    }
    const minutes = Math.floor(remainingSeconds / 60);
    const seconds = String(remainingSeconds % 60).padStart(2, '0');
    pairingStatus.textContent = `Pairing code: ${activePairingCode} (${minutes}:${seconds} remaining)`;
}

// Rabindra Neupane
// refreshPairingStatus displays the active code and its local expiration time.
async function refreshPairingStatus() {
    // Stop when the pairing status area is not available.
    const pairingStatus = document.getElementById('pairing-status');
    if (!pairingStatus) return;
    try {
        const status = await GetCoordinatorPairingStatus();
        if (!status.active) {
            activePairingCode = '';
            pairingExpiresAt = 0;
            pairingStatus.textContent = 'Pairing is closed.';
            return;
        }
        activePairingCode = status.code ?? '';
        pairingExpiresAt = status.expiresAt ? new Date(status.expiresAt).getTime() : 0;
        renderPairingCountdown();
    } catch (error) {
        pairingStatus.textContent = `Cannot read pairing status: ${error}`;
    }
}
// No address needed when this computer is the coordinator itself
coordinatorMode?.addEventListener('change', () => {
    registrationSettingsEdited = true;
    updateCoordinatorSecurityControls(coordinatorMode.checked);
});
clientMode?.addEventListener('change', () => {
    registrationSettingsEdited = true;
    updateCoordinatorSecurityControls(!clientMode.checked);
});
coordinatorAddress?.addEventListener('input', () => {
    registrationSettingsEdited = true;
    showPendingRole(false);
});
pairingCodeInput?.addEventListener('input', () => {
    // Keep the one-time code numeric and at most six digits.
    pairingCodeInput.value = pairingCodeInput.value.replace(/\D/g, '').slice(0, 6);
    registrationSettingsEdited = true;
});
saveCoordinatorButton?.addEventListener('click', async () => {
    saveCoordinatorButton.disabled = true;
    try {
        await ConfigureCoordinator(coordinatorMode?.checked ?? false, coordinatorAddress?.value ?? '', pairingCodeInput?.value ?? '');
        registrationSettingsEdited = false;
        showingDiscovery = false;
        await refreshRegistration();
    } catch (error) {
        const status = document.getElementById('registration-status');
        if (status) status.textContent = friendlyConnectionError(error);
        showConnectionHelp(error);
    } finally {
        saveCoordinatorButton.disabled = false;
    }
});

// Start a short single-use pairing window when requested by the coordinator.
startPairingButton?.addEventListener('click', async () => {
    startPairingButton.disabled = true;
    try {
        await StartCoordinatorPairing();
        await refreshPairingStatus();
    } catch (error) {
        const pairingStatus = document.getElementById('pairing-status');
        if (pairingStatus) pairingStatus.textContent = `Cannot start pairing: ${error}`;
    } finally {
        startPairingButton.disabled = false;
    }
});
// Registered Devices goes back from the discovery list to the normal device list
document.getElementById('registered-devices-button')?.addEventListener('click', () => {
    showingDiscovery = false;
    void refreshRegistration();
});

// Rabindra Neupane
// Files picked in the list, and where they were picked from (shown in the label)
const selectedFileNames = new Set<string>();
let selectionSource = 'Synced Files';
// The Recent Activity text
const recentFileActivity = document.getElementById('recent-activity')

// Rabindra Neupane
let transferInProgress = false;
// Turns off Sync Now and Send Whole File while a transfer is running
function setTransferBusy(busy: boolean) {
    transferInProgress = busy;
    for (const id of ['sync-now-button', 'send-whole-file-button']) {
        const button = document.querySelector<HTMLButtonElement>(`#${id}`);
        if (button) button.disabled = busy;
    }
}

// Rabindra Neupane
const syncNowButton = document.querySelector<HTMLButtonElement>('#sync-now-button');
const wholeFileButton = document.querySelector<HTMLButtonElement>('#send-whole-file-button');
// Sends the selected files to the selected device one by one and lists how each one went.
// wholeFile true = send the full file, false = only send the chunks that changed.
async function transferSelectedFiles(wholeFile: boolean) {
    // Ignore extra clicks, and make sure files and a device are picked
    if (transferInProgress) return;
    if (selectedFileNames.size === 0 || !selectedDeviceAddress) {
        if (recentFileActivity) recentFileActivity.textContent = 'Please select files and a device first.';
        return;
    }
    const names = [...selectedFileNames];
    const address = selectedDeviceAddress;
    const button = wholeFile ? wholeFileButton : syncNowButton;
    const label = button?.textContent ?? '';
    const outcomes: string[] = [];
    setTransferBusy(true);
    if (button) button.textContent = wholeFile ? 'Sending files...' : 'Syncing...';
    try {
        for (const [index, name] of names.entries()) {
            // Show progress like "2/5: Syncing report.docx..." under the results so far
            if (recentFileActivity) recentFileActivity.textContent = [...outcomes, `${index + 1}/${names.length}: ${wholeFile ? 'Sending' : 'Syncing'} ${name}...`].join('\n');
            try {
                // Same messages for both buttons, based on the receiver's conflict check (Santosh)
                const result = wholeFile ? await SendFile(address, name) : await SyncFile(address, name);
                if (result.keepLocal) {
                    outcomes.push(`${name}: receiver kept its newer version. Pull it before sending this older copy.`);
                } else if (result.conflictFile) {
                    outcomes.push(`Conflict for ${name}. Receiver kept its file; incoming copy: ${result.conflictFile}`);
                } else {
                    const details = wholeFile ? 'Whole file verified on receiver.'
                        : `Verified on receiver. Sent ${result.chunksSent} of ${result.totalChunks} chunks (${result.chunkBytesSent} bytes of chunk data; excludes metadata and HTTP overhead).`;
                    // baseUpdated is false when the file was edited while it was being sent
                    outcomes.push(`${name}: ${details}${result.baseUpdated ? '' : ' Local file changed during transfer; latest edits are not confirmed synced.'}`);
                }
            } catch (error) {
                outcomes.push(`Failed to ${wholeFile ? 'send' : 'sync'} ${name}: ${error}`);
            }
        }
    } finally {
        // Put the button back to normal and show every result
        setTransferBusy(false);
        if (button) button.textContent = label;
        if (recentFileActivity) recentFileActivity.textContent = outcomes.join('\n');
    }
}
syncNowButton?.addEventListener('click', () => { void transferSelectedFiles(false); });
wholeFileButton?.addEventListener('click', () => { void transferSelectedFiles(true); });
// Rabindra Neupane
// File picker implementation: Nisham Karki. Working-folder selection: Rabindra Neupane.
const selectFileButton = document.querySelector<HTMLButtonElement>('#select-file-button');
let fileListRefreshing = false;
// Remembers the last file list so the table only redraws when something changed
let fileListSignature = '';
// Rabindra Neupane
// Makes the scroll area five rows tall, so longer lists scroll inside the table
function sizeFileList() {
    const viewport = document.querySelector<HTMLElement>('.file-table-scroll');
    const table = document.querySelector<HTMLTableElement>('.file-table');
    if (!viewport || !table) return;
    const rows = table.tBodies[0]?.rows;
    if (!rows?.length) return;
    const lastVisibleRow = rows[Math.min(5, rows.length) - 1];
    const height = lastVisibleRow.getBoundingClientRect().bottom - table.getBoundingClientRect().top;
    viewport.style.maxHeight = `${Math.ceil(height)}px`;
}
//Rabindra Neupane
// Resize again whenever the table changes size, and stop watching when the window closes
const fileTableObserver = new ResizeObserver(sizeFileList);
const fileTable = document.querySelector('.file-table');
if (fileTable) fileTableObserver.observe(fileTable);
window.addEventListener('beforeunload', () => fileTableObserver.disconnect());


// Rabindra Neupane
// Updates the "selected files" label and ticks the matching checkboxes
function updateFileSelection() {
    const label = document.getElementById('selected-files');
    if (label) label.textContent = selectedFileNames.size
        ? `${selectionSource} (${selectedFileNames.size} selected): ${[...selectedFileNames].join(', ')}`
        : 'No files selected';
    document.querySelectorAll<HTMLInputElement>('#synced-file-list input[type="checkbox"]').forEach(input => {
        input.checked = selectedFileNames.has(input.value);
        input.closest('tr')?.classList.toggle('selected-file', input.checked);
    });
}
// Rabindra Neupane
// Reads the Synced Files folder and redraws the table, but only when something actually changed
async function refreshSyncedFiles() {
    // Skip if the last refresh is still running
    if (fileListRefreshing) return;
    fileListRefreshing = true;
    const status = document.getElementById('file-list-status');
    try {
        const files = await ListSyncedFiles();
        // Drop selections for files that aren't in the folder anymore
        for (const name of selectedFileNames) {
            if (!files.some(file => file.name === name)) selectedFileNames.delete(name);
        }
        updateFileSelection();
        // Compare with last time so the table isn't rebuilt every 2 seconds
        const signature = JSON.stringify(files);
        if (signature !== fileListSignature) {
            const body = document.getElementById('synced-file-list');
            if (!body) return;
            body.replaceChildren();
            for (const file of files) {
                const row = document.createElement('tr');
                const nameCell = document.createElement('td');
                const label = document.createElement('label');
                // Named radio, but it's a checkbox so more than one file can be picked
                const radio = document.createElement('input');
                radio.type = 'checkbox';
                radio.name = 'sync-file';
                radio.value = file.name;
                radio.checked = selectedFileNames.has(file.name);
                row.classList.toggle('selected-file', radio.checked);
                radio.addEventListener('change', () => {
                    if (radio.checked) selectedFileNames.add(file.name);
                    else selectedFileNames.delete(file.name);
                    selectionSource = 'Synced Files';
                    updateFileSelection();
                });
                label.append(radio, document.createTextNode(` ${file.name}`));
                nameCell.appendChild(label);
                row.appendChild(nameCell);
                // Status, Size and Modified columns
                for (const text of ['Available locally', `${file.size.toLocaleString()} bytes`, new Date(file.modified).toLocaleString()]) {
                    const cell = document.createElement('td');
                    cell.textContent = text;
                    row.appendChild(cell);
                }
                body.appendChild(row);
            }
            sizeFileList();
            fileListSignature = signature;
        }
        if (status) status.textContent = files.length ? `${files.length} file(s) available` : 'Synced Files is empty. Add a file from your computer.';
    } catch (error) {
        if (status) status.textContent = `Cannot load Synced Files: ${error}`;
    } finally {
        fileListRefreshing = false;
    }
}

//Rabindra Neupane
// "Add files from computer" button. The added files get selected right away so they can be synced.
selectFileButton?.addEventListener('click', async () => {
    selectFileButton.disabled = true;
    try {
        const result = await AddFiles();
        await refreshSyncedFiles();
        if (result.names.length) {
            selectedFileNames.clear();
            result.names.forEach(name => selectedFileNames.add(name));
            selectionSource = 'File picker - working copies in Synced Files';
            updateFileSelection();
        }
        if (result.errors.length && recentFileActivity) recentFileActivity.textContent = result.errors.join('\n');
    } catch (error) {
        if (recentFileActivity) recentFileActivity.textContent = `Cannot add file: ${error}`;
    } finally {
        selectFileButton.disabled = false;
    }
});

//Rabindra Neupane
// Load the file list now, then check the folder again every 2 seconds
void refreshSyncedFiles();
const fileListTimer = window.setInterval(() => { void refreshSyncedFiles(); }, 2000);
window.addEventListener('beforeunload', () => window.clearInterval(fileListTimer));
///// Nisham Karki
// Last few file changes, newest first
let recentActivites: string[] = []

// fileWatcher.go sends this whenever a file in Synced Files changes
EventsOn('file-change', (modifiedFile: string) => {
    void refreshSyncedFiles();
    // Keep the most recent activity on top
    recentActivites.unshift(modifiedFile)

    // Only keep the last 5
    recentActivites = recentActivites.slice(0, 5)

    // Show the new list, unless a transfer is using the activity box right now
    if (recentFileActivity && !transferInProgress) {
        recentFileActivity.textContent = recentActivites.join('\n')
    }
})

// Rabindra Neupane
// Check registration every 5 seconds. This is separate from Find Coordinator,
// which only scans the network when it's clicked.
void refreshRegistration();
const registrationRefreshTimer = window.setInterval(() => { void refreshRegistration(); }, 5000);
const pairingCountdownTimer = window.setInterval(renderPairingCountdown, 1000);
window.addEventListener('beforeunload', () => {
    window.clearInterval(registrationRefreshTimer);
    window.clearInterval(pairingCountdownTimer);
});

// Santosh Tripathee - sets up the Conflicts button, panel and pop-up notice (conflict-panel.ts)
setupConflictPanel();
