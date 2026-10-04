(function(){const n=document.createElement("link").relList;if(n&&n.supports&&n.supports("modulepreload"))return;for(const o of document.querySelectorAll('link[rel="modulepreload"]'))d(o);new MutationObserver(o=>{for(const i of o)if(i.type==="childList")for(const a of i.addedNodes)a.tagName==="LINK"&&a.rel==="modulepreload"&&d(a)}).observe(document,{childList:!0,subtree:!0});function l(o){const i={};return o.integrity&&(i.integrity=o.integrity),o.referrerPolicy&&(i.referrerPolicy=o.referrerPolicy),o.crossOrigin==="use-credentials"?i.credentials="include":o.crossOrigin==="anonymous"?i.credentials="omit":i.credentials="same-origin",i}function d(o){if(o.ep)return;o.ep=!0;const i=l(o);fetch(o.href,i)}})();function u(){return window.go.main.App.AddFile()}function f(){return window.go.main.App.DiscoverDevices()}function y(){return window.go.main.App.GetLocalIP()}function v(e,n){return window.go.main.App.SendFile(e,n)}function p(e,n){return window.go.main.App.SendFirstChunk(e,n)}function h(e,n,l){return window.runtime.EventsOnMultiple(e,n,l)}function g(e,n){return h(e,n,-1)}document.querySelector("#app").innerHTML=`
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
            <button id="test-chunk-button">Send First Chunk</button>

            <div>
                <button>Conflicts</button>
                <button>Settings</button>
            </div>
        </footer>

    </div>
`;async function m(){try{const e=await y();console.log("FileSync local IP address:"),console.log(e)}catch(e){console.error("FileSync failed to get local IP address",e)}}m();async function S(){const e=document.getElementById("connected-devices");if(e){e.innerHTML="<p>Searching for FileSync devices...</p>";try{console.log("Searching for FileSync devices on LAN...");const n=await f();if(console.log("Discovered devices:",n),console.log("FileSync devices Found:"),console.log(n),!n||n.length===0){e.innerHTML="<p>No FileSync devices found.</p>";return}e.innerHTML="",n.forEach(l=>{const d=document.createElement("div");d.className="device",d.innerHTML=`<span>${l.name}</span>
                                        <span>${l.online?"online":"offline"}</span>`,d.addEventListener("click",()=>{document.querySelectorAll(".device").forEach(o=>o.classList.remove("selected-device")),d.classList.add("selected-device"),s=`${l.ip}:${l.port}`,console.log("Selected FileSync Device: ",l.name,s)}),e.appendChild(d)})}catch(n){console.error("FileSync failed to discover devices",n),e.innerHTML="<p>Device scan failed.</p>"}}}const F=document.getElementById("scan-devices-button");F?.addEventListener("click",()=>{S()});let s=null,c=null;const t=document.getElementById("recent-activity"),b=document.getElementById("sync-now-button");b?.addEventListener("click",async()=>{if(!c){console.log("Please select a file to sync first."),t&&(t.textContent="Please select a file to sync first.");return}if(!s){console.log("Please select a device to sync first."),t&&(t.textContent="Please select a device to sync first.");return}try{console.log("Sending file:",c,"to device:",s),t&&(t.textContent=`Sending ${c} to ${s}...`),await v(s,c),t&&(t.textContent=`${c} synced successfully to ${s}`),console.log("File sent successfully")}catch(e){t&&(t.textContent=`Failed to sync ${c} to ${s}`),console.error("Failed to send file",e)}});const w=document.getElementById("select-file-button"),L=document.getElementById("selected-files");w?.addEventListener("click",async()=>{c=await u(),c&&(L.textContent=c),console.log("Selected File: ",c)});let r=[];g("file-change",e=>{r.unshift(e),r=r.slice(0,5),t&&(t.textContent=r.join(`
`))});const P=document.getElementById("test-chunk-button");P?.addEventListener("click",async()=>{if(!c){console.log("Please select a file firsr."),t&&(t.textContent="Please select a file first.");return}if(!s){console.log("Please select a FileSync device first."),t&&(t.textContent="Please select a device first.");return}try{console.log("Sending first chunk of",c,"to",s),t&&(t.textContent=`Sending first chunk of ${c}...`),await p(s,c),console.log("First chunk sent successfully."),t&&(t.textContent=`First chunk of ${c} sent successfully.`)}catch(e){console.error("Failed to send first chunk:",e),t&&(t.textContent=`Failed to send first chunk of ${c}.`)}});
