(function(){const n=document.createElement("link").relList;if(n&&n.supports&&n.supports("modulepreload"))return;for(const t of document.querySelectorAll('link[rel="modulepreload"]'))d(t);new MutationObserver(t=>{for(const i of t)if(i.type==="childList")for(const s of i.addedNodes)s.tagName==="LINK"&&s.rel==="modulepreload"&&d(s)}).observe(document,{childList:!0,subtree:!0});function c(t){const i={};return t.integrity&&(i.integrity=t.integrity),t.referrerPolicy&&(i.referrerPolicy=t.referrerPolicy),t.crossOrigin==="use-credentials"?i.credentials="include":t.crossOrigin==="anonymous"?i.credentials="omit":i.credentials="same-origin",i}function d(t){if(t.ep)return;t.ep=!0;const i=c(t);fetch(t.href,i)}})();function r(){return window.go.main.App.AddFile()}function a(){return window.go.main.App.DiscoverDevices()}function u(){return window.go.main.App.GetLocalIP()}function f(e,n,c){return window.runtime.EventsOnMultiple(e,n,c)}function p(e,n){return f(e,n,-1)}document.querySelector("#app").innerHTML=`
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

                <p id="recent-activity">No Files Modified</p>
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
`;async function v(){try{const e=await u();console.log("FileSync local IP address:"),console.log(e)}catch(e){console.error("FileSync failed to get local IP address",e)}}v();async function h(){try{console.log("Searching for FileSync devices on LAN...");const e=await a();console.log("FileSync devices Found:"),console.log(e)}catch(e){console.error("FileSync failed to discover devices",e)}}h();const y=document.getElementById("select-file-button"),m=document.getElementById("selected-files"),l=document.getElementById("recent-activity");y?.addEventListener("click",async()=>{const e=await r();m.textContent=e});let o=[];p("file-change",e=>{o.unshift(e),o=o.slice(0,5),l&&(l.textContent=o.join(`
`))});
