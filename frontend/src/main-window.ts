// AddFile runs the file picking window from filepicker.go
import { AddFile, DiscoverDevices, GetLocalIP, SendFile } from '../wailsjs/go/main/App';
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
                <button id="scan-devices-button">Scan Devices</button>

                <div id="connected-devices">
                    <p>No scan Performed</p>
                </div>
            </div>

            <div class="section activity">
                <h2>Recent Activity</h2>

                <p id="recent-activity">No Files Modified</p>
            </div>

        </section>

        <footer class="footer">
            <button id="sync-now-button">Sync Now</button>

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

/// Rabindra Neupane
//scanDevices function handles the scanning of devices and updates the UI accordingly.
async function scanDevices() {
    const connectedDevices = document.getElementById('connected-devices');
    // Get the container element for connected devices
    if (!connectedDevices) {
        return;
    }
    // Clear any previous scan results and show a searching message
    connectedDevices.innerHTML = '<p>Searching for FileSync devices...</p>';
    // Start the scanning process for FileSync devices on the LAN
    try {
        console.log("Searching for FileSync devices on LAN...");

        const devices = await DiscoverDevices();
        console.log("Discovered devices:", devices);

        console.log("FileSync devices Found:");
        console.log(devices);
        // If no devices are found, update the UI accordingly
        if(!devices || devices.length === 0) {
            connectedDevices.innerHTML = '<p>No FileSync devices found.</p>';

            return;
        }
        connectedDevices.innerHTML = '';
        // Populate the UI with the list of discovered devices
        devices.forEach((device) => {
            const deviceElement = document.createElement('div');
            deviceElement.className = 'device';

            // Create a new div element for each device and display its IP address and status
            deviceElement.innerHTML =  `<span>${device.name}</span>
                                        <span>${device.online ? 'online' : 'offline'}</span>`;

            // Add a click event listener to each device element to handle selection
            deviceElement.addEventListener('click', () => {
                document.querySelectorAll('.device').forEach((element) => element.classList.remove('selected-device'));
                
                deviceElement.classList.add('selected-device');

                selectedDeviceAddress = `${device.ip}:${device.port}`;
                console.log("Selected FileSync Device: ", device.name, selectedDeviceAddress);
            });

            connectedDevices.appendChild(deviceElement);
        })
    }
    catch (error) {
        console.error("FileSync failed to discover devices", error);
        connectedDevices.innerHTML = '<p>Device scan failed.</p>';
    }
}
/// Rabindra Neupane
/// Event listener for the "Scan Devices" button
const scanDevicesButton = document.getElementById('scan-devices-button');
scanDevicesButton?.addEventListener('click', () => {scanDevices()});

/// Selected device information
let selectedDeviceAddress: string | null = null;
/// Selected file information
let selectedFileName: string | null = null;

const syncNowButton = document.getElementById('sync-now-button');
syncNowButton?.addEventListener('click', async () => {
    // If no file is selected, prompt the user to select a file first.
    if (!selectedFileName) {
        console.log("Please select a file to sync first.");
        return;
    }
    // If no device is selected, prompt the user to select a device first.
    if (!selectedDeviceAddress) {
        console.log("Please select a device to sync first.");
        return;
    }

    try {
        console.log("Sending file:", selectedFileName, "to device:", selectedDeviceAddress);
        await SendFile(selectedDeviceAddress, selectedFileName);
        console.log("File sent successfully");
    }
    catch (error) {
        console.error("Failed to send file", error);
        }
});

///// Nisham Karki
// Makes button clickable by recognizing button id
const selectFileButton = document.getElementById('select-file-button')
// Use this to replace the "no file selected" with the name of the file selected
const selectedFileText = document.getElementById('selected-files')
// Use to populate "Recent activity" section with recently modified files
const recentFileActivity = document.getElementById('recent-activity')

// Event listener for when user click on "Select File"
selectFileButton?.addEventListener('click', async () => {

    // Call the AddFile() function and get the name of the file selected
    // through file picker window
    selectedFileName = await AddFile()

    // Return the name of the file selected and replace "no file selected"
    if(selectedFileName) {
        selectedFileText!.textContent = selectedFileName
    }
    console.log("Selected File: ", selectedFileName)
})

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
