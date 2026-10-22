
import {
    AddFile,
    DiscoverDevices,
    GetLocalIP,
    SendFile,
    SendAllChunks,
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

            <button id = "select-file-button">Select file</button>
        </section>

        <section class="file-details">
            <h2>Files</h2>

            <table class="file-table">
                <thead>
                    <tr>
                        <th>Name</th>
                        <th>Status</th>
                        <th>Size</th>
                        <th>Modified</th>
                    </tr>
                </thead>

                <tbody>
                    <tr>
                        <td>project.doc</td>
                        <td>Synced</td>
                        <td>4.2 MB</td>
                        <td>1:42 PM</td>
                    </tr>

                    <tr>
                        <td>project.zip</td>
                        <td>Syncing 63%</td>
                        <td>82.5 MB</td>
                        <td>1:39 PM</td>
                    </tr>

                    <tr>
                        <td>presentation.dox</td>
                        <td>Conflict</td>
                        <td>12.3 MB</td>
                        <td>1:35 PM</td>
                    </tr>
                </tbody>
            </table>
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
            <button id="send-whole-file-button">Send Whole File</button>
            <button id="send-all-chunks-button">Send All Chunk</button>

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
let selectedFileName: string | null = null;
// Use to populate "Recent activity" section with recently modified files
const recentFileActivity = document.getElementById('recent-activity')

//Rabindra Neupane
let transferInProgress = false;
function setTransferBusy(busy: boolean) {
    transferInProgress = busy;
    for (const id of ['sync-now-button', 'send-all-chunks-button', 'send-whole-file-button']) {
        const button = document.querySelector<HTMLButtonElement>(`#${id}`);
        if (button) button.disabled = busy;
    }
}

//Rabindra Neupane
const syncNowButton = document.querySelector<HTMLButtonElement>('#sync-now-button');
syncNowButton?.addEventListener('click', async () => {
    //Rabindra Neupane
    if (transferInProgress) return;
    // If no file is selected, prompt the user to select a file first.
    if (!selectedFileName) {
        console.log("Please select a file to sync first.");

        // Update the recent activity section to inform the user
        if (recentFileActivity) {
            recentFileActivity.textContent = "Please select a file to sync first.";
        }

        return;
    }
    // If no device is selected, prompt the user to select a device first.
    if (!selectedDeviceAddress) {
        console.log("Please select a device to sync first.");

        // Update the recent activity section to inform the user
        if (recentFileActivity) {
            recentFileActivity.textContent = "Please select a device to sync first.";
        }

        return;
    }

    //Rabindra Neupane
    const transferFileName = selectedFileName;
    const transferDeviceAddress = selectedDeviceAddress;
    const buttonLabel = syncNowButton.textContent;
    setTransferBusy(true);
    syncNowButton.textContent = 'Syncing...';
    try {
        console.log("Syncing changed chunks of:", transferFileName, "to device:", transferDeviceAddress);

        // Update the recent activity section to inform the user that sending is in progress
        if (recentFileActivity) {
            //Rabindra Neupane
            recentFileActivity.textContent = `Checking and syncing ${transferFileName}...`;
        }

        //Rabindra Neupane
        const result = await SyncFile(transferDeviceAddress, transferFileName);
		//Rabindra Neupane
		if (result.conflictFile) {
			if (recentFileActivity) recentFileActivity.textContent = `Conflict for ${transferFileName}. Receiver kept its current file and saved the incoming copy at ${result.conflictFile}.`;
			return;
		}

        // Update the recent activity section to inform the user that the file has been synced successfully
        if (recentFileActivity) {
            //Rabindra Neupane
            recentFileActivity.textContent = `${transferFileName} verified on receiver. Sent ${result.chunksSent} of ${result.totalChunks} chunks (${result.chunkBytesSent} bytes of chunk data; excludes metadata and HTTP overhead).`;
        }
        console.log("File sent successfully");
    }
    catch (error) {
        // Update the recent activity section to inform the user that the sync failed
        if (recentFileActivity) {
            //Rabindra Neupane
            recentFileActivity.textContent = `Failed to sync ${transferFileName}: ${error}`;
        }

        console.error("Failed to send file", error);
    }
    //Rabindra Neupane
    finally {
        setTransferBusy(false);
        syncNowButton.textContent = buttonLabel;
    }
});

//Rabindra Neupane
// Keep whole-file transfer available for the proposal's performance comparison.
const wholeFileButton = document.querySelector<HTMLButtonElement>('#send-whole-file-button');
wholeFileButton?.addEventListener('click', async () => {
    if (transferInProgress) return;
    if (!selectedFileName || !selectedDeviceAddress) {
        if (recentFileActivity) recentFileActivity.textContent = 'Please select a file and device first.';
        return;
    }
    const fileName = selectedFileName;
    const address = selectedDeviceAddress;
    const label = wholeFileButton.textContent;
    setTransferBusy(true);
    wholeFileButton.textContent = 'Sending file...';
    try {
        if (recentFileActivity) recentFileActivity.textContent = `Sending the whole file ${fileName}...`;
        await SendFile(address, fileName);
        if (recentFileActivity) recentFileActivity.textContent = `Whole file ${fileName} received in Synced Files.`;
    } catch (error) {
        if (recentFileActivity) recentFileActivity.textContent = `Whole-file transfer failed: ${error}`;
    } finally {
        setTransferBusy(false);
        wholeFileButton.textContent = label;
    }
});

///// Nisham Karki
// Makes button clickable by recognizing button id
const selectFileButton = document.getElementById('select-file-button')
// Use this to replace the "no file selected" with the name of the file selected
const selectedFileText = document.getElementById('selected-files')

// Event listener for when user click on "Select File"
selectFileButton?.addEventListener('click', async () => {

    // Call the AddFile() function and get the name of the file selected
    // through file picker window
    selectedFileName = await AddFile()

    // Return the name of the file selected and replace "no file selected"
    if (selectedFileName) {
        selectedFileText!.textContent = selectedFileName
    }
    console.log("Selected File: ", selectedFileName)
})

///// Nisham Karki
// Array to store list of recent activities
let recentActivites: string[] = []

EventsOn('file-change', (modifiedFile: string) => {
    // Keep the most recent activity on top
    recentActivites.unshift(modifiedFile)

    // Keep only 5 most recent activities at a time.
    // We will show only the 5 most recent activites for now
    recentActivites = recentActivites.slice(0, 5)

    // THis will replace the currently displayed activities with any newer activity
    if (recentFileActivity) {
        recentFileActivity.textContent = recentActivites.join('\n')
    }
})

//Rabindra Neupane
// Removed the unused first-chunk button handler.
///Rabindra Neupane
// Send all chunks of the selected file to the selected device
//Rabindra Neupane
const sendAllChunksButton = document.querySelector<HTMLButtonElement>('#send-all-chunks-button');
sendAllChunksButton?.addEventListener('click', async () => {

    //Rabindra Neupane
    if (transferInProgress) {
        return;
    }

    // Send all chunks of the selected file to the selected device
    if(! selectedFileName) {
        console.log("Please select a file first.");

        if (recentFileActivity) {
            recentFileActivity.textContent = "Please select a file to sync first.";
        }
        return;
    }

    // Check if a FileSync device is selected
    if(! selectedDeviceAddress) {
        console.log("Please select a FileSync device first.");

        if (recentFileActivity) {
            recentFileActivity.textContent = "Please select a device first.";
        }
        return;
    }

    //Rabindra Neupane
    // Lock the button before awaiting the transfer and keep its original label.
    const buttonLabel = sendAllChunksButton.textContent;
    const transferFileName = selectedFileName;
    const transferDeviceAddress = selectedDeviceAddress;
    setTransferBusy(true);
    sendAllChunksButton.textContent = 'Sending chunks...';

    // Attempt to send all chunks of the selected file to the selected device
    try {
        //Rabindra Neupane
        console.log("Sending all chunks of", transferFileName, "to", transferDeviceAddress);

        // Update the recent file activity to indicate that sending is in progress
        if (recentFileActivity) {
            //Rabindra Neupane
            recentFileActivity.textContent = `Sending all chunks of ${transferFileName}...`;
        }

        //Rabindra Neupane
        await SendAllChunks(transferDeviceAddress, transferFileName);

        if (recentFileActivity) {
            //Rabindra Neupane
            recentFileActivity.textContent = `All chunks of ${transferFileName} sent successfully.`;
        }
        console.log("All chunks sent successfully.");
    }
    catch (error) {
        console.error("Failed to send all chunks:", error);
        if (recentFileActivity) {
            //Rabindra Neupane
            recentFileActivity.textContent = `Failed to send all chunks of ${transferFileName}: ${error}`;
        }
    }
    //Rabindra Neupane
    finally {
        setTransferBusy(false);
        sendAllChunksButton.textContent = buttonLabel;
    }
});

//Rabindra Neupane
// Poll registration separately from explicit subnet scans; avoid overlapping requests.
void refreshRegistration();
const registrationRefreshTimer = window.setInterval(() => { void refreshRegistration(); }, 5000);
window.addEventListener('beforeunload', () => window.clearInterval(registrationRefreshTimer));