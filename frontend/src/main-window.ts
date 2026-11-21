// Author: Rabindra Neupane
// Main FileSync window: file list, devices, recent activity and the sync buttons.
// Nisham, Rabindra and Santosh all have parts in here, marked by name below.

// Go functions from app.go that Wails makes available to the frontend
import {
    AddFiles,
    ListSyncedFiles,
    DiscoverDevices,
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

        <nav class="dashboard-tabs" aria-label="FileSync sections">
            <button id="sync-dashboard-tab" class="dashboard-tab active-tab" type="button" aria-controls="sync-dashboard-panel" aria-selected="true">
                Sync Dashboard
            </button>
            <button id="connections-tab" class="dashboard-tab" type="button" aria-controls="connections-panel" aria-selected="false">
                Connections
                <span id="connection-tab-status" class="tab-status-dot" aria-hidden="true"></span>
            </button>
        </nav>

        <main id="sync-dashboard-panel" class="app-tab-panel" aria-labelledby="sync-dashboard-tab">
            <div id="connection-banner" class="connection-banner" role="status" hidden>
                <span id="connection-banner-message">Connect this device before syncing files.</span>
                <button id="open-connections-button" type="button">Open Connections</button>
            </div>
            <section id="sync-destination-section" class="sync-destination-section">
                <div class="destination-heading">
                    <h2>Send to a device</h2>
                    <p>Choose where the selected files should go.</p>
                </div>
                <div class="destination-controls">
                    <label for="sync-destination-select">Destination</label>
                    <select id="sync-destination-select">
                        <option value="">Select an online device</option>
                    </select>
                    <p id="sync-destination-status" class="destination-status" role="status">No destination selected.</p>
                </div>
            </section>
        </main>
        <main id="connections-panel" class="app-tab-panel" aria-labelledby="connections-tab" hidden></main>

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
                <div class="connection-panel">
                    <fieldset class="role-options">
                        <legend>Choose this computer's role</legend>
                        <label class="role-card">
                            <input type="radio" id="coordinator-mode" name="device-role">
                            <span><strong>Coordinator</strong><small>Create and manage the FileSync network.</small></span>
                        </label>
                        <label class="role-card">
                            <input type="radio" id="client-mode" name="device-role" checked>
                            <span><strong>Client</strong><small>Connect to an existing coordinator.</small></span>
                        </label>
                    </fieldset>
                    <div id="client-connection-controls" class="setup-card">
                        <div class="setup-heading">
                            <h3>Connect to a coordinator</h3>
                            <p>Find the coordinator or enter its address.</p>
                        </div>
                        <div class="field-group">
                            <label for="coordinator-address">Coordinator address</label>
                            <input id="coordinator-address" placeholder="192.168.1.10:8080" autocomplete="off">
                        </div>
                        <button id="find-coordinator-button" class="secondary-action">Find Coordinator</button>
                        <div id="coordinator-search-results" aria-live="polite"></div>
                        <div class="field-group">
                            <label id="pairing-code-label" for="pairing-code">Six-digit pairing code</label>
                            <input id="pairing-code" maxlength="6" inputmode="numeric" placeholder="000 000" autocomplete="one-time-code">
                        </div>
                        <p class="connection-guidance">Ask the coordinator to allow a new device, then enter its code.</p>
                        <div id="pairing-lockout" class="pairing-lockout" aria-live="assertive" hidden></div>
                    </div>
                    <button id="save-coordinator-button" class="primary-action">Connect</button>
                    <div id="coordinator-pairing-controls" class="setup-card" hidden>
                        <div class="setup-heading">
                            <h3>Add a client</h3>
                            <p>Open a temporary window for one new device.</p>
                        </div>
                        <button id="start-pairing-button">Allow New Device for 2 Minutes</button>
                        <div id="pairing-status" class="pairing-code-card" aria-live="polite">
                            <strong id="pairing-code-display">Pairing closed</strong>
                            <span id="pairing-countdown">Create a code when the client is ready.</span>
                        </div>
                    </div>
                    <section class="connection-summary">
                        <h3>This device</h3>
                        <div id="registration-status" aria-live="polite">Loading device registration...</div>
                        <div id="connection-help" aria-live="polite"></div>
                    </section>
                </div>
                <section class="registered-devices-section">
                    <div class="registered-devices-heading">
                        <div><h3>Registered devices</h3><p>Select an online device as the sync destination.</p></div>
                        <button id="registered-devices-button" class="secondary-action">Refresh</button>
                    </div>
                    <div id="connected-devices">
                        <p>Registered devices will appear after coordinator setup.</p>
                    </div>
                </section>
            </div>

            <div class="section activity">
                <h2>Recent Activity</h2>

                <p id="recent-activity">No Files Modified</p>
                <details id="transfer-error-details" hidden>
                    <summary>Technical details</summary>
                    <pre id="transfer-error-text"></pre>
                </details>
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
// organizeDashboardPanels separates daily syncing from connection setup.
function organizeDashboardPanels() {
    // Move existing sections into their matching tab without recreating their controls.
    const syncPanel = document.getElementById('sync-dashboard-panel');
    const connectionsPanel = document.getElementById('connections-panel');
    const filePicker = document.querySelector('.select-file-to-sync');
    const fileDetails = document.querySelector('.file-details');
    const destinationSection = document.querySelector('.sync-destination-section');
    const deviceSection = document.querySelector('.devices');
    const activitySection = document.querySelector('.activity');
    const footer = document.querySelector('.footer');
    const oldSplitLayout = document.querySelector('.progress-activity');
    if (!syncPanel || !connectionsPanel) return;

    // Keep file work, activity, and sync actions together on the dashboard.
    if (filePicker) syncPanel.appendChild(filePicker);
    if (fileDetails) syncPanel.appendChild(fileDetails);
    if (destinationSection) syncPanel.appendChild(destinationSection);
    if (activitySection) syncPanel.appendChild(activitySection);
    if (footer) syncPanel.appendChild(footer);

    // Keep all role, pairing, and device-management controls on Connections.
    if (deviceSection) connectionsPanel.appendChild(deviceSection);
    oldSplitLayout?.remove();
}

// Rabindra Neupane
// showDashboardTab displays one workspace and updates accessible tab state.
function showDashboardTab(tab: 'sync' | 'connections') {
    // Find both tab buttons and panels.
    const syncButton = document.querySelector<HTMLButtonElement>('#sync-dashboard-tab');
    const connectionsButton = document.querySelector<HTMLButtonElement>('#connections-tab');
    const syncPanel = document.getElementById('sync-dashboard-panel');
    const connectionsPanel = document.getElementById('connections-panel');
    if (!syncButton || !connectionsButton || !syncPanel || !connectionsPanel) return;

    // Show only the selected panel and mark its button as active.
    const showingSync = tab === 'sync';
    syncPanel.hidden = !showingSync;
    connectionsPanel.hidden = showingSync;
    syncButton.classList.toggle('active-tab', showingSync);
    connectionsButton.classList.toggle('active-tab', !showingSync);
    syncButton.setAttribute('aria-selected', String(showingSync));
    connectionsButton.setAttribute('aria-selected', String(!showingSync));
}

// Build the two workspaces before attaching control behavior.
organizeDashboardPanels();
document.getElementById('sync-dashboard-tab')?.addEventListener('click', () => showDashboardTab('sync'));
document.getElementById('connections-tab')?.addEventListener('click', () => showDashboardTab('connections'));
document.getElementById('open-connections-button')?.addEventListener('click', () => showDashboardTab('connections'));
// Rabindra Neupane
// Scans the network for a coordinator. Only runs when the user clicks Find Coordinator.
async function findCoordinator() {
    // Don't start a second scan while one is already running
    if (coordinatorSearchRunning) return;
    coordinatorSearchRunning = true;
    showingDiscovery = true;
    selectedDeviceAddress = null;
    const container = document.getElementById('coordinator-search-results');
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
let pairingBlockedUntil = 0;
let pairingAttemptsRemaining = 5;
let registrationTabInitialized = false;
let latestRegisteredDevices: ListedDevice[] = [];
let registrationRetryUntil = 0;

// Rabindra Neupane
// A device as it comes back from the coordinator
type ListedDevice = { id: string; name: string; ip: string; port: number; online: boolean; lastSeen?: string; coordinator?: boolean };

// Rabindra Neupane
// listedDeviceAddress formats an IPv4 or IPv6 address for network requests.
function listedDeviceAddress(device: ListedDevice): string {
    // Add brackets around IPv6 addresses before attaching the port.
    return device.ip.includes(':') ? `[${device.ip}]:${device.port}` : `${device.ip}:${device.port}`;
}

// Rabindra Neupane
// renderSyncDestinations lists online peers on the Sync Dashboard.
function renderSyncDestinations(devices: ListedDevice[]) {
    // Save the latest list so the destination dropdown can update the shared selection.
    latestRegisteredDevices = devices;
    const destinationSelect = document.querySelector<HTMLSelectElement>('#sync-destination-select');
    const destinationStatus = document.getElementById('sync-destination-status');
    if (!destinationSelect || !destinationStatus) return;

    // Rebuild the list from online devices other than this computer.
    destinationSelect.replaceChildren();
    const placeholder = document.createElement('option');
    placeholder.value = '';
    placeholder.textContent = 'Select an online device';
    destinationSelect.appendChild(placeholder);
    for (const device of devices.filter(device => device.online)) {
        const option = document.createElement('option');
        option.value = device.id;
        option.textContent = `${device.name || 'Unknown device'} — ${listedDeviceAddress(device)}`;
        destinationSelect.appendChild(option);
    }

    // Keep a valid selected device after the coordinator refreshes the list.
    const selectedDevice = devices.find(device => device.id === selectedDeviceID && device.online);
    if (selectedDevice) {
        destinationSelect.value = selectedDevice.id;
        selectedDeviceAddress = listedDeviceAddress(selectedDevice);
        destinationStatus.textContent = `Selected: ${selectedDevice.name || 'Unknown device'} (${selectedDeviceAddress})`;
        return;
    }

    // Clear a destination that is missing or no longer online.
    selectedDeviceID = null;
    selectedDeviceAddress = null;
    destinationSelect.value = '';
    destinationStatus.textContent = devices.some(device => device.online)
        ? 'No destination selected.'
        : 'No other online devices are available.';
}

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
    // Keep coordinator search results separate from registered sync devices.
    const containerID = showingDiscovery ? 'coordinator-search-results' : 'connected-devices';
    const container = document.getElementById(containerID);
    if (!container) return;
    const previousAddress = selectedDeviceAddress;
    // Preserve the sync destination while showing coordinator discovery results.
    if (!showingDiscovery) selectedDeviceAddress = null;
    container.replaceChildren();
    // Leave this computer out of its own list and put online devices first
    const peers = devices
        .filter(device => !ownDeviceID || device.id !== ownDeviceID)
        .sort((a, b) => Number(b.online) - Number(a.online));
    if (peers.length === 0) {
        if (!showingDiscovery) renderSyncDestinations([]);
        container.textContent = showingDiscovery ? 'No coordinator found. Start one on another computer or enter its address manually.' : 'No other registered devices yet.';
        return;
    }
    for (const device of peers) {
        // IPv6 addresses need square brackets before the port
        const address = listedDeviceAddress(device);
        const row = document.createElement('button');
        row.type = 'button';
        row.className = 'device';
        row.dataset.deviceId = device.id;
        row.disabled = !device.online;
        // Show the last-seen value in the timezone of the computer viewing the list.
        if (device.lastSeen) row.title = `Last seen: ${formatLastSeen(device.lastSeen)}\nDevice ID: ${device.id}`;
        // Discovery chooses setup details; only registered rows choose sync targets.
        if (showingDiscovery) {
            appendDetailRows(row, [
                ['Coordinator', device.name || 'Unknown device'],
                ['Address', address],
                ['Status', 'Available — click to select']
            ]);
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
            renderSyncDestinations(peers);
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
    // Keep the dashboard dropdown synchronized with the registered-device list.
    if (!showingDiscovery) renderSyncDestinations(peers);
}

// Select the same destination whether it is chosen on Dashboard or Connections.
document.getElementById('sync-destination-select')?.addEventListener('change', event => {
    // Match the selected device ID to the coordinator's latest online list.
    const destinationSelect = event.currentTarget as HTMLSelectElement;
    const selectedDevice = latestRegisteredDevices.find(device => device.id === destinationSelect.value && device.online);
    selectedDeviceID = selectedDevice?.id ?? null;
    selectedDeviceAddress = selectedDevice ? listedDeviceAddress(selectedDevice) : null;

    // Update the status text and the matching card in Connections.
    const destinationStatus = document.getElementById('sync-destination-status');
    if (destinationStatus) {
        destinationStatus.textContent = selectedDevice
            ? `Selected: ${selectedDevice.name || 'Unknown device'} (${selectedDeviceAddress})`
            : 'No destination selected.';
    }
    document.querySelectorAll<HTMLElement>('#connected-devices .device').forEach(deviceCard => {
        deviceCard.classList.toggle('selected-device', deviceCard.dataset.deviceId === selectedDeviceID);
    });
});

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
    if (message.includes('pairing code is invalid') || message.includes('pairing code is incorrect')) {
        return 'Incorrect pairing code. Check the code shown on the coordinator.';
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
// friendlyTransferError identifies the selected destination instead of the coordinator.
function friendlyTransferError(error: unknown): string {
    const message = String(error ?? '').toLowerCase();
    if (message.includes('deadline exceeded') || message.includes('timed out')) {
        return 'Destination device did not respond. Confirm FileSync is open and both computers are on the same network.';
    }
    if (message.includes('connection refused')) {
        return 'The destination was found, but FileSync is not accepting connections on port 8080.';
    }
    return String(error ?? 'Transfer failed.');
}

// Rabindra Neupane
// updateConnectionNavigation shows connection health without mixing setup into syncing.
function updateConnectionNavigation(status: Awaited<ReturnType<typeof GetRegistrationStatus>>) {
    // Update the small status indicator in the Connections tab.
    const statusDot = document.getElementById('connection-tab-status');
    const connectionBanner = document.getElementById('connection-banner');
    const bannerMessage = document.getElementById('connection-banner-message');
    statusDot?.classList.toggle('connected', status.registered);
    statusDot?.classList.toggle('attention', !status.registered);

    // Show a dashboard shortcut whenever syncing is not ready.
    if (connectionBanner) connectionBanner.hidden = status.registered;
    if (bannerMessage && !status.registered) {
        bannerMessage.textContent = status.state === 'revoked'
            ? 'This registration was removed. Pair the device again in Connections.'
            : status.state === 'pairing-required'
                ? 'Enter a valid pairing code in Connections before syncing.'
                : 'Finish connection setup before syncing files.';
    }

    // Choose the most useful tab only once when the application starts.
    if (!registrationTabInitialized) {
        showDashboardTab(status.registered ? 'sync' : 'connections');
        registrationTabInitialized = true;
    }
}

// Rabindra Neupane
// Shows this computer's role, the coordinator address and whether it's registered
function renderRegistrationStatus(
    container: HTMLElement,
    status: Awaited<ReturnType<typeof GetRegistrationStatus>>
) {
    // Reflect connection readiness in the navigation and Sync Dashboard banner.
    updateConnectionNavigation(status);

    // Keep the client lockout display synchronized with the coordinator response.
    pairingAttemptsRemaining = status.pairingAttemptsRemaining ?? 5;
    pairingBlockedUntil = status.pairingBlockedUntil
        ? new Date(status.pairingBlockedUntil).getTime()
        : 0;
    // Convert the backend retry delay into a local deadline for a smooth countdown.
    registrationRetryUntil = !status.registered && status.retryInSeconds > 0
        ? Date.now() + status.retryInSeconds * 1000
        : 0;
    renderPairingLockout();

    // Nothing set up yet, so just tell the user what to do
    if (!status.configured) {
        container.classList.remove('registration-details');
        container.textContent = 'Choose this computer as coordinator, or enter the coordinator address.';
        return;
    }

    // Translate the backend lifecycle state into one clear instruction.
    const stateMessages: Record<string, string> = {
        'not-configured': 'Not configured',
        'pairing-required': pairingBlockedUntil > Date.now()
            ? 'Registration temporarily blocked'
            : pairingAttemptsRemaining < 5
                ? `Incorrect pairing code — ${pairingAttemptsRemaining} attempts remaining`
                : 'Pairing required — enter a new code and click Connect',
        'registering': 'Registering with the coordinator',
        'registered': 'Securely connected',
        'reconnecting': status.retryInSeconds > 0
            ? `Coordinator unavailable — retrying in ${status.retryInSeconds} seconds`
            : 'Coordinator unavailable — retrying',
        'revoked': 'Registration removed — pair this device again'
    };
    const registrationState = stateMessages[status.state]
        ?? (status.lastError ? friendlyConnectionError(status.lastError) : 'Connecting');
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
            retryMessage.id = 'registration-retry-message';
            retryMessage.textContent = pairingBlockedUntil > Date.now()
                ? 'Registration is paused until the lockout timer reaches zero.'
                : status.state === 'pairing-required' || status.state === 'revoked'
                    ? 'Automatic registration is paused until a new pairing code is submitted.'
                    : status.retryInSeconds > 0
                        ? `Registration will retry automatically in about ${status.retryInSeconds} seconds.`
                        : 'Registration will retry automatically.';
            const details = document.createElement('details');
            const summary = document.createElement('summary');
            summary.textContent = 'Technical details';
            const technicalError = document.createElement('pre');
            technicalError.textContent = status.lastError;
            details.append(summary, technicalError);
            connectionHelp.append(retryMessage, details);
            renderRegistrationRetryCountdown();
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
        // Restore only the saved role; keep the client address field ready for a new connection.
        if (!registrationSettingsLoaded && !registrationSettingsEdited && mode && address) {
            mode.checked = status.coordinator;
            if (clientMode) clientMode.checked = !status.coordinator;
            address.value = '';
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
        if ((!status.configured || !status.registered) && !showingDiscovery) renderDevices([]);
        if (status.configured && status.registered && !showingDiscovery) {
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
// renderPairingLockout keeps the coordinator-controlled five-minute timer visible.
function renderPairingLockout() {
    // Read the client controls that must remain locked during the timeout.
    const lockoutMessage = document.getElementById('pairing-lockout');
    const clientSelected = clientMode?.checked ?? false;
    const remainingSeconds = Math.max(0, Math.ceil((pairingBlockedUntil - Date.now()) / 1000));
    const blocked = clientSelected && remainingSeconds > 0;
    if (!lockoutMessage) return;

    // Disable new attempts until the coordinator's deadline has passed.
    if (pairingCodeInput) pairingCodeInput.disabled = blocked;
    if (saveCoordinatorButton) saveCoordinatorButton.disabled = blocked;
    lockoutMessage.hidden = !blocked;

    // Show a stable minutes-and-seconds countdown while registration is blocked.
    if (blocked) {
        const minutes = Math.floor(remainingSeconds / 60);
        const seconds = String(remainingSeconds % 60).padStart(2, '0');
        lockoutMessage.replaceChildren();
        const heading = document.createElement('strong');
        heading.textContent = 'Too many incorrect pairing attempts.';
        const countdown = document.createElement('span');
        countdown.textContent = `Try again in ${minutes}:${seconds}`;
        lockoutMessage.append(heading, countdown);
        return;
    }

    // Clear an expired lockout and allow the client to submit a new code.
    if (pairingBlockedUntil > 0 && remainingSeconds === 0) pairingBlockedUntil = 0;
    lockoutMessage.replaceChildren();
}

// Rabindra Neupane
// renderRegistrationRetryCountdown continuously shows the next automatic retry.
function renderRegistrationRetryCountdown() {
    // Keep special pairing instructions unchanged while pairing is paused or blocked.
    const retryMessage = document.getElementById('registration-retry-message');
    if (!retryMessage || pairingBlockedUntil > Date.now() || registrationRetryUntil <= 0) return;

    // Calculate the remaining whole seconds from the latest coordinator deadline.
    const remainingSeconds = Math.max(0, Math.ceil((registrationRetryUntil - Date.now()) / 1000));
    retryMessage.textContent = remainingSeconds > 0
        ? `Registration will retry automatically in ${remainingSeconds} ${remainingSeconds === 1 ? 'second' : 'seconds'}.`
        : 'Registration retrying now...';
}

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
    const coordinatorStarted = coordinator && ownDeviceIsCoordinator && !registrationSettingsEdited;
    if (clientControls) clientControls.hidden = coordinator;
    // Pairing is available only after this device is running as coordinator.
    if (coordinatorControls) coordinatorControls.hidden = !coordinatorStarted;
    if (coordinatorMode) coordinatorMode.checked = coordinator;
    if (clientMode) clientMode.checked = !coordinator;
    if (coordinatorAddress) coordinatorAddress.disabled = coordinator;
    if (saveCoordinatorButton) {
        saveCoordinatorButton.textContent = coordinator ? 'Start Coordinator' : 'Connect';
        saveCoordinatorButton.hidden = coordinatorStarted;
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
    const pairingCodeDisplay = document.getElementById('pairing-code-display');
    const pairingCountdown = document.getElementById('pairing-countdown');
    if (!pairingCodeDisplay || !pairingCountdown || !activePairingCode || pairingExpiresAt <= 0) return;

    // Calculate a readable minutes-and-seconds countdown.
    const remainingSeconds = Math.max(0, Math.ceil((pairingExpiresAt - Date.now()) / 1000));
    if (remainingSeconds === 0) {
        pairingCodeDisplay.textContent = 'Pairing closed';
        pairingCountdown.textContent = 'Code expired. Create another code when the client is ready.';
        activePairingCode = '';
        pairingExpiresAt = 0;
        return;
    }
    const minutes = Math.floor(remainingSeconds / 60);
    const seconds = String(remainingSeconds % 60).padStart(2, '0');
    pairingCodeDisplay.textContent = activePairingCode.replace(/(\d{3})(\d{3})/, '$1 $2');
    pairingCountdown.textContent = `${minutes}:${seconds} remaining`;
}

// Rabindra Neupane
// refreshPairingStatus displays the active code and its local expiration time.
async function refreshPairingStatus() {
    // Stop when the pairing status area is not available.
    const pairingCodeDisplay = document.getElementById('pairing-code-display');
    const pairingCountdown = document.getElementById('pairing-countdown');
    if (!pairingCodeDisplay || !pairingCountdown) return;
    try {
        const status = await GetCoordinatorPairingStatus();
        if (!status.active) {
            activePairingCode = '';
            pairingExpiresAt = 0;
            pairingCodeDisplay.textContent = 'Pairing closed';
            pairingCountdown.textContent = 'Create a code when the client is ready.';
            return;
        }
        activePairingCode = status.code ?? '';
        pairingExpiresAt = status.expiresAt ? new Date(status.expiresAt).getTime() : 0;
        renderPairingCountdown();
    } catch (error) {
        pairingCodeDisplay.textContent = 'Pairing unavailable';
        pairingCountdown.textContent = friendlyConnectionError(error);
    }
}
// No address needed when this computer is the coordinator itself
coordinatorMode?.addEventListener('change', () => {
    registrationSettingsEdited = true;
    updateCoordinatorSecurityControls(coordinatorMode.checked);
});
clientMode?.addEventListener('change', () => {
    registrationSettingsEdited = true;
    // Start each manual client setup with a blank field and visible example.
    if (clientMode.checked && coordinatorAddress) coordinatorAddress.value = '';
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
        const searchResults = document.getElementById('coordinator-search-results');
        if (searchResults) searchResults.replaceChildren();
        await refreshRegistration();
    } catch (error) {
        const status = document.getElementById('registration-status');
        if (status) status.textContent = friendlyConnectionError(error);
        showConnectionHelp(error);
    } finally {
        saveCoordinatorButton.disabled = false;
        renderPairingLockout();
    }
});

// Start a short single-use pairing window when requested by the coordinator.
startPairingButton?.addEventListener('click', async () => {
    startPairingButton.disabled = true;
    try {
        await StartCoordinatorPairing();
        await refreshPairingStatus();
    } catch (error) {
        const pairingCodeDisplay = document.getElementById('pairing-code-display');
        const pairingCountdown = document.getElementById('pairing-countdown');
        if (pairingCodeDisplay) pairingCodeDisplay.textContent = 'Cannot create code';
        if (pairingCountdown) pairingCountdown.textContent = friendlyConnectionError(error);
    } finally {
        startPairingButton.disabled = false;
    }
});
// Registered Devices goes back from the discovery list to the normal device list
document.getElementById('registered-devices-button')?.addEventListener('click', () => {
    showingDiscovery = false;
    const searchResults = document.getElementById('coordinator-search-results');
    if (searchResults) searchResults.replaceChildren();
    void refreshRegistration();
});

// Rabindra Neupane
// Files picked in the list, and where they were picked from (shown in the label)
const selectedFileNames = new Set<string>();
let selectionSource = 'Synced Files';
// The Recent Activity text
const recentFileActivity = document.getElementById('recent-activity')
const transferErrorDetails = document.querySelector<HTMLDetailsElement>('#transfer-error-details');
const transferErrorText = document.querySelector<HTMLElement>('#transfer-error-text');

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

    // Recheck the selected peer immediately before sending any file data.
    if (selectedDeviceID) {
        try {
            const registeredDevices = await GetRegisteredDevices();
            const selectedDevice = registeredDevices.find(device => device.id === selectedDeviceID);
            if (!selectedDevice?.online) {
                selectedDeviceID = null;
                selectedDeviceAddress = null;
                if (recentFileActivity) {
                    recentFileActivity.textContent = 'The selected device is offline. Refresh the device list and choose an online device.';
                }
                await refreshRegistration();
                return;
            }
        } catch (error) {
            if (recentFileActivity) recentFileActivity.textContent = `Cannot verify the selected device: ${friendlyConnectionError(error)}`;
            return;
        }
    }
    const names = [...selectedFileNames];
    const address = selectedDeviceAddress;
    const button = wholeFile ? wholeFileButton : syncNowButton;
    const label = button?.textContent ?? '';
    const outcomes: string[] = [];
    const technicalErrors: string[] = [];
    if (transferErrorDetails) transferErrorDetails.hidden = true;
    if (transferErrorText) transferErrorText.textContent = '';
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
                outcomes.push(`Failed to ${wholeFile ? 'send' : 'sync'} ${name}: ${friendlyTransferError(error)}`);
                technicalErrors.push(`${name}: ${String(error ?? 'Unknown transfer error')}`);
            }
        }
    } finally {
        // Put the button back to normal and show every result
        setTransferBusy(false);
        if (button) button.textContent = label;
        if (recentFileActivity) recentFileActivity.textContent = outcomes.join('\n');
        if (technicalErrors.length > 0 && transferErrorDetails && transferErrorText) {
            transferErrorText.textContent = technicalErrors.join('\n');
            transferErrorDetails.hidden = false;
        }
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
const pairingLockoutTimer = window.setInterval(renderPairingLockout, 1000);
const registrationRetryTimer = window.setInterval(renderRegistrationRetryCountdown, 1000);
window.addEventListener('beforeunload', () => {
    window.clearInterval(registrationRefreshTimer);
    window.clearInterval(pairingCountdownTimer);
    window.clearInterval(pairingLockoutTimer);
    window.clearInterval(registrationRetryTimer);
});

// Santosh Tripathee - sets up the Conflicts button, panel and pop-up notice (conflict-panel.ts)
setupConflictPanel();
