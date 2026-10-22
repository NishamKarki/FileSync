
import {
    AddFiles,
    //Rabindra Neupane
    ListSyncedFiles,
    DiscoverDevices,
    GetLocalIP,
    SendFile,
    //Rabindra Neupane
    SyncFile,
    //Rabindra Neupane
    ConfigureCoordinator,
    GetRegistrationStatus,
    GetRegisteredDevices
} from '../wailsjs/go/main/App';

// EventsOn calls runtime event from file modification detected by fileWatcher.go
import { EventsOn } from '../wailsjs/runtime/runtime';

import './main-window.css'; // CSS Style for this main window

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

                        <!-- Rabindra Neupane -->
            <div>
                <button id="select-file-button">Add files from computer</button>
                <p>Add files to Synced Files.</p>
            </div>
        </section>

        <section class="file-details">
            <!-- Rabindra Neupane -->
            <h2>Files in Synced Files</h2>
            <p>Select one or more files below to sync their working copies.</p>
            <p id="file-list-status" role="status"></p>

            <!-- Rabindra Neupane -->
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
                <!-- Rabindra Neupane -->
                <div class="coordinator-controls">
                    <label><input type="checkbox" id="coordinator-mode"> This computer is the coordinator</label>
                    <label for="coordinator-address">Coordinator IP:port</label>
                    <input id="coordinator-address" placeholder="192.168.1.10:8080" autocomplete="off">
                    <button id="save-coordinator-button">Apply Coordinator Settings</button>
                    <p id="registration-status" aria-live="polite">Loading device registration...</p>
                </div>
                <!-- Rabindra Neupane -->
                <button id="find-coordinator-button">Find Coordinator</button>
                <!-- Rabindra Neupane -->
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
            <!-- Rabindra Neupane -->
            <details class="transfer-diagnostics">
                <summary>Transfer diagnostics</summary>
                <div>
                    <button id="send-whole-file-button">Send Whole File</button>
                </div>
            </details>

            <div>
                <button>Conflicts</button>
                <button>Settings</button>
            </div>
        </footer>

    </div>
`
///Rabindra Neupane
// Test the GetLocalIP function and log the result
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

//Rabindra Neupane
// Discovery is used only to choose a coordinator during setup.
async function findCoordinator() {
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
        if (showingDiscovery) renderDevices((devices || []).filter(device => device.coordinator));
    } catch (error) {
        if (showingDiscovery && container) container.textContent = `Coordinator search failed: ${error}`;
    } finally {
        coordinatorSearchRunning = false;
        if (button) button.disabled = false;
    }
}
//Rabindra Neupane
 document.getElementById('find-coordinator-button')?.addEventListener('click', () => { void findCoordinator(); });
/// Selected device information
let selectedDeviceAddress: string | null = null;
//Rabindra Neupane
let selectedDeviceID: string | null = null;
let showingDiscovery = false;
//Rabindra Neupane
let coordinatorSearchRunning = false;
let registrationRefreshRunning = false;
let registrationSettingsLoaded = false;
let registrationSettingsEdited = false;
let ownDeviceID = '';

//Rabindra Neupane
type ListedDevice = { id: string; name: string; ip: string; port: number; online: boolean; lastSeen?: string; coordinator?: boolean };

//Rabindra Neupane
function renderDevices(devices: ListedDevice[]) {
    const container = document.getElementById('connected-devices');
    if (!container) return;
    const previousAddress = selectedDeviceAddress;
    selectedDeviceAddress = null;
    container.replaceChildren();
    //Rabindra Neupane
    // Keep online devices first, preserving the existing order within each group.
    const peers = devices
        .filter(device => !ownDeviceID || device.id !== ownDeviceID)
        .sort((a, b) => Number(b.online) - Number(a.online));
    if (peers.length === 0) {
        container.textContent = showingDiscovery ? 'No coordinator found. Start one on another computer or enter its address manually.' : 'No other registered devices yet.';
        return;
    }
    for (const device of peers) {
        const address = device.ip.includes(':') ? `[${device.ip}]:${device.port}` : `${device.ip}:${device.port}`;
        const row = document.createElement('button');
        row.type = 'button';
        row.className = 'device';
        row.disabled = !device.online;
        //Rabindra Neupane
        row.textContent = `Device name: ${device.name}\nDevice ID: ${device.id || 'Not assigned'}\nAddress: ${address} · ${device.online ? 'online' : 'offline'}${device.coordinator ? ' · Coordinator' : ''}`;
        if (device.lastSeen) row.title = `Last seen: ${device.lastSeen}\nDevice ID: ${device.id}`;
        //Rabindra Neupane
        // Discovery chooses setup details; only registered rows choose sync targets.
        if (showingDiscovery) {
            row.textContent = `${device.name} - Coordinator - ${address} - Use this coordinator`;
            row.addEventListener('click', () => {
                const mode = document.querySelector<HTMLInputElement>('#coordinator-mode');
                const input = document.querySelector<HTMLInputElement>('#coordinator-address');
                if (!mode || !input) return;
                mode.checked = false;
                input.disabled = false;
                input.value = address;
                registrationSettingsEdited = true;
                const label = document.getElementById('registration-status');
                if (label) label.textContent = 'Coordinator selected. Click Apply Coordinator Settings to register.';
            });
            container.appendChild(row);
            continue;
        }
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
        container.appendChild(row);
    }
}

//Rabindra Neupane
async function refreshRegistration() {
    if (registrationRefreshRunning) return;
    registrationRefreshRunning = true;
    const label = document.getElementById('registration-status');
    try {
        const status = await GetRegistrationStatus();
        ownDeviceID = status.deviceId;
        const mode = document.querySelector<HTMLInputElement>('#coordinator-mode');
        const address = document.querySelector<HTMLInputElement>('#coordinator-address');
        if (!registrationSettingsLoaded && !registrationSettingsEdited && mode && address) {
            mode.checked = status.coordinator;
            address.value = status.coordinatorAddress;
            address.disabled = mode.checked;
        }
        registrationSettingsLoaded = true;
        //Rabindra Neupane
        const deviceDetails = status.coordinator
            ? `Coordinator: ${status.deviceName} (${status.coordinatorAddress || 'connecting'})`
            : `Client: ${status.deviceName} - Coordinator: ${status.coordinatorAddress || 'connecting'}`;
        if (label) {
            label.textContent = status.configured
                ? `${deviceDetails} · ${status.registered ? 'registered' : status.lastError || 'connecting'}${status.deviceId ? ` · Device ID: ${status.deviceId}` : ''}`
                : 'Choose this computer as coordinator, or enter the coordinator address.';
        }
        //Rabindra Neupane
        if (!status.configured && !showingDiscovery) renderDevices([]);
        if (status.configured && !showingDiscovery) {
            const devices = await GetRegisteredDevices();
            if (!showingDiscovery) renderDevices(devices);
        }
    } catch (error) {
        if (label) label.textContent = `Registration: ${error}`;
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
const coordinatorMode = document.querySelector<HTMLInputElement>('#coordinator-mode');
const coordinatorAddress = document.querySelector<HTMLInputElement>('#coordinator-address');
coordinatorMode?.addEventListener('change', () => {
    registrationSettingsEdited = true;
    if (coordinatorAddress) coordinatorAddress.disabled = coordinatorMode.checked;
});
coordinatorAddress?.addEventListener('input', () => { registrationSettingsEdited = true; });
const saveCoordinatorButton = document.querySelector<HTMLButtonElement>('#save-coordinator-button');
saveCoordinatorButton?.addEventListener('click', async () => {
    saveCoordinatorButton.disabled = true;
    try {
        await ConfigureCoordinator(coordinatorMode?.checked ?? false, coordinatorAddress?.value ?? '');
        showingDiscovery = false;
        await refreshRegistration();
    } catch (error) {
        const status = document.getElementById('registration-status');
        if (status) status.textContent = `Cannot apply settings: ${error}`;
    } finally {
        saveCoordinatorButton.disabled = false;
    }
});
document.getElementById('registered-devices-button')?.addEventListener('click', () => {
    showingDiscovery = false;
    void refreshRegistration();
});

// Selected file information
//Rabindra Neupane
const selectedFileNames = new Set<string>();
let selectionSource = 'Synced Files';
// Use to populate "Recent activity" section with recently modified files
const recentFileActivity = document.getElementById('recent-activity')

//Rabindra Neupane
let transferInProgress = false;
function setTransferBusy(busy: boolean) {
    transferInProgress = busy;
    for (const id of ['sync-now-button', 'send-whole-file-button']) {
        const button = document.querySelector<HTMLButtonElement>(`#${id}`);
        if (button) button.disabled = busy;
    }
}

//Rabindra Neupane
// Process a fixed selection sequentially; each file retains independent results.
const syncNowButton = document.querySelector<HTMLButtonElement>('#sync-now-button');
const wholeFileButton = document.querySelector<HTMLButtonElement>('#send-whole-file-button');
async function transferSelectedFiles(wholeFile: boolean) {
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
            if (recentFileActivity) recentFileActivity.textContent = [...outcomes, `${index + 1}/${names.length}: ${wholeFile ? 'Sending' : 'Syncing'} ${name}...`].join('\n');
            try {
                if (wholeFile) {
                    await SendFile(address, name);
                    outcomes.push(`Whole file ${name} received in Synced Files.`);
                } else {
                    const result = await SyncFile(address, name);
                    outcomes.push(result.conflictFile
                        ? `Conflict for ${name}. Receiver kept its file; incoming copy: ${result.conflictFile}`
                        : `${name} verified on receiver. Sent ${result.chunksSent} of ${result.totalChunks} chunks (${result.chunkBytesSent} bytes of chunk data; excludes metadata and HTTP overhead).`);
                }
            } catch (error) {
                outcomes.push(`Failed to ${wholeFile ? 'send' : 'sync'} ${name}: ${error}`);
            }
        }
    } finally {
        setTransferBusy(false);
        if (button) button.textContent = label;
        if (recentFileActivity) recentFileActivity.textContent = outcomes.join('\n');
    }
}
syncNowButton?.addEventListener('click', () => { void transferSelectedFiles(false); });
wholeFileButton?.addEventListener('click', () => { void transferSelectedFiles(true); });
//Rabindra Neupane
// File picker implementation: Nisham Karki. Working-folder selection: Rabindra Neupane.
const selectFileButton = document.querySelector<HTMLButtonElement>('#select-file-button');
let fileListRefreshing = false;
let fileListSignature = '';


//Rabindra Neupane
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
//Rabindra Neupane
async function refreshSyncedFiles() {
    if (fileListRefreshing) return;
    fileListRefreshing = true;
    const status = document.getElementById('file-list-status');
    try {
        const files = await ListSyncedFiles();
        //Rabindra Neupane
        for (const name of selectedFileNames) {
            if (!files.some(file => file.name === name)) selectedFileNames.delete(name);
        }
        updateFileSelection();
        const signature = JSON.stringify(files);
        if (signature !== fileListSignature) {
            const body = document.getElementById('synced-file-list');
            if (!body) return;
            body.replaceChildren();
            for (const file of files) {
                const row = document.createElement('tr');
                const nameCell = document.createElement('td');
                const label = document.createElement('label');
                const radio = document.createElement('input');
                //Rabindra Neupane
                radio.type = 'checkbox';
                radio.name = 'sync-file';
                radio.value = file.name;
                radio.checked = selectedFileNames.has(file.name);
                row.classList.toggle('selected-file', radio.checked);
                //Rabindra Neupane
                radio.addEventListener('change', () => {
                    if (radio.checked) selectedFileNames.add(file.name);
                    else selectedFileNames.delete(file.name);
                    selectionSource = 'Synced Files';
                    updateFileSelection();
                });
                label.append(radio, document.createTextNode(` ${file.name}`));
                nameCell.appendChild(label);
                row.appendChild(nameCell);
                for (const text of ['Available locally', `${file.size.toLocaleString()} bytes`, new Date(file.modified).toLocaleString()]) {
                    const cell = document.createElement('td');
                    cell.textContent = text;
                    row.appendChild(cell);
                }
                body.appendChild(row);
            }
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
selectFileButton?.addEventListener('click', async () => {
    selectFileButton.disabled = true;
    try {
        //Rabindra Neupane
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
void refreshSyncedFiles();
const fileListTimer = window.setInterval(() => { void refreshSyncedFiles(); }, 2000);
window.addEventListener('beforeunload', () => window.clearInterval(fileListTimer));
///// Nisham Karki
// Array to store list of recent activities
let recentActivites: string[] = []

EventsOn('file-change', (modifiedFile: string) => {
    //Rabindra Neupane
    void refreshSyncedFiles();
    // Keep the most recent activity on top
    recentActivites.unshift(modifiedFile)

    // Keep only 5 most recent activities at a time.
    // We will show only the 5 most recent activites for now
    recentActivites = recentActivites.slice(0, 5)

    // THis will replace the currently displayed activities with any newer activity
    if (recentFileActivity && !transferInProgress) {
        recentFileActivity.textContent = recentActivites.join('\n')
    }
})

//Rabindra Neupane
// Poll registration separately from explicit subnet scans; avoid overlapping requests.
void refreshRegistration();
const registrationRefreshTimer = window.setInterval(() => { void refreshRegistration(); }, 5000);
window.addEventListener('beforeunload', () => window.clearInterval(registrationRefreshTimer));