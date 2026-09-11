import './main-window.css'

document.querySelector<HTMLDivElement>('#app')!.innerHTML = `
    <div class="app">

        <header class="title">
            <h1>FileSync</h1>
        </header>

        <section class="select-file-to-sync">
            <div>
                <h2>Choose file to sync</h2>
                <p class="file-path">(Link file path here like "C:\Some File")</p>
            </div>

            <button>Select file</button>
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