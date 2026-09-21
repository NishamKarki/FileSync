import './main-window.css'
import { AddFile, GetLocalIP, DiscoverDevices } from '../wailsjs/go/main/App'

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

                <div class="device">
                    <span>Nisham-PC</span>
                    <span>Online</span>
                </div>

                <div class="device">
                    <span>Rabindra-PC</span>
                    <span>Online</span>
                </div>

                <div class="device">
                    <span>Santosh-PC</span>
                    <span>Offline</span>
                </div>
            </div>

            <div class="section activity">
                <h2>Recent Activity</h2>

                <p><span>1:42</span> report.docx modified</p>
                <p><span>1:42</span> 1 modified chunk detected</p>
                <p><span>1:43</span> Synchronization completed</p>
                <p><span>1:43</span> Integrity verification passed</p>
            </div>

        </section>

        <footer class="footer">
            <button>Sync Now</button>

            <div>
                <button>Conflicts</button>
                <button>Settings</button>
            </div>
        </footer>

    </div>
`
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

// Test the DiscoverDevices function and log the result
async function testDiscoverDevices() {
    try {
        console.log("Searching for FileSync devices on LAN...")

        const devices = await DiscoverDevices()
        
        console.log("FileSync devices Found:")
        console.log(devices);
    }
    catch (error) {
        console.error("FileSync failed to discover devices", error);
    }
}
testDiscoverDevices();

// Makes button clickable by recognizing button id
const selectFileButton = document.getElementById('select-file-button')
// Use this to replace the "no file selected" with the name of the file selected
const selectedFileText = document.getElementById('selected-files')

// Event listener for when user click on "Select File"
selectFileButton?.addEventListener('click', async () => {

    // Call the AddFile() function and get the name of the file selected
    // through file picker window
    const selectedFileName = await AddFile()

    // If user canceled or no files were added, return nothing
    // if (selectedFileName == "") {
    //     return
    // }

    // Return the name of the file selected and replace "no file selected"
    selectedFileText!.textContent = selectedFileName
})
