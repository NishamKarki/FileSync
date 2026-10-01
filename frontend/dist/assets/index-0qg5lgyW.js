(function(){const t=document.createElement("link").relList;if(t&&t.supports&&t.supports("modulepreload"))return;for(const n of document.querySelectorAll('link[rel="modulepreload"]'))i(n);new MutationObserver(n=>{for(const c of n)if(c.type==="childList")for(const r of c.addedNodes)r.tagName==="LINK"&&r.rel==="modulepreload"&&i(r)}).observe(document,{childList:!0,subtree:!0});function o(n){const c={};return n.integrity&&(c.integrity=n.integrity),n.referrerPolicy&&(c.referrerPolicy=n.referrerPolicy),n.crossOrigin==="use-credentials"?c.credentials="include":n.crossOrigin==="anonymous"?c.credentials="omit":c.credentials="same-origin",c}function i(n){if(n.ep)return;n.ep=!0;const c=o(n);fetch(n.href,c)}})();function u(){return window.go.main.App.AddFile()}function f(){return window.go.main.App.DiscoverDevices()}function v(){return window.go.main.App.GetLocalIP()}function p(e,t){return window.go.main.App.SendFile(e,t)}function y(e,t,o){return window.runtime.EventsOnMultiple(e,t,o)}function h(e,t){return y(e,t,-1)}document.querySelector("#app").innerHTML=`
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
`;async function m(){try{const e=await v();console.log("FileSync local IP address:"),console.log(e)}catch(e){console.error("FileSync failed to get local IP address",e)}}m();async function g(){const e=document.getElementById("connected-devices");if(e){e.innerHTML="<p>Searching for FileSync devices...</p>";try{console.log("Searching for FileSync devices on LAN...");const t=await f();if(console.log("Discovered devices:",t),console.log("FileSync devices Found:"),console.log(t),!t||t.length===0){e.innerHTML="<p>No FileSync devices found.</p>";return}e.innerHTML="",t.forEach(o=>{const i=document.createElement("div");i.className="device",i.innerHTML=`<span>${o.name}</span>
                                        <span>${o.online?"online":"offline"}</span>`,i.addEventListener("click",()=>{document.querySelectorAll(".device").forEach(n=>n.classList.remove("selected-device")),i.classList.add("selected-device"),l=`${o.ip}:${o.port}`,console.log("Selected FileSync Device: ",o.name,l)}),e.appendChild(i)})}catch(t){console.error("FileSync failed to discover devices",t),e.innerHTML="<p>Device scan failed.</p>"}}}const S=document.getElementById("scan-devices-button");S?.addEventListener("click",()=>{g()});let l=null,s=null;const b=document.getElementById("sync-now-button");b?.addEventListener("click",async()=>{if(!s){console.log("Please select a file to sync first.");return}if(!l){console.log("Please select a device to sync first.");return}try{console.log("Sending file:",s,"to device:",l),await p(l,s),console.log("File sent successfully")}catch(e){console.error("Failed to send file",e)}});const F=document.getElementById("select-file-button"),L=document.getElementById("selected-files"),a=document.getElementById("recent-activity");F?.addEventListener("click",async()=>{s=await u(),s&&(L.textContent=s),console.log("Selected File: ",s)});let d=[];h("file-change",e=>{d.unshift(e),d=d.slice(0,5),a&&(a.textContent=d.join(`
`))});
