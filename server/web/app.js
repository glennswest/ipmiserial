let servers = [];
let currentServer = null;

// Per-server sessions: { name: { terminal, fitAddon, eventSource, currentLogFile, lastLogCount } }
const serverSessions = {};

// URL hash support for direct linking: #server1 or #server1/live or #server1/history
function parseHash() {
    const hash = window.location.hash.slice(1); // remove #
    if (!hash) return null;
    const parts = hash.split('/');
    return {
        server: parts[0],
        tab: parts[1] || 'live'
    };
}

function updateHash(server, tab) {
    const newHash = tab === 'live' ? server : `${server}/${tab}`;
    if (window.location.hash !== '#' + newHash) {
        history.replaceState(null, '', '#' + newHash);
    }
}

async function fetchServers() {
    try {
        const response = await fetch('/api/servers');
        const newServers = await response.json();

        // Check if server list changed
        const serverNames = newServers.map(s => s.name).sort().join(',');
        const oldServerNames = servers.map(s => s.name).sort().join(',');

        if (serverNames !== oldServerNames) {
            servers = newServers;
            renderServerTabs();
        } else {
            // Just update status
            servers = newServers;
            updateServerStatus();
        }
    } catch (error) {
        console.error('Failed to fetch servers:', error);
    }
}

function renderServerTabs() {
    const tabsContainer = document.getElementById('server-tabs');
    const contentContainer = document.getElementById('server-content');

    if (servers.length === 0) {
        tabsContainer.innerHTML = '<li class="nav-item"><span class="nav-link text-muted">No servers found</span></li>';
        contentContainer.innerHTML = '';
        return;
    }

    // Build tabs
    tabsContainer.innerHTML = servers.map((server, index) => `
        <li class="nav-item">
            <a class="nav-link ${index === 0 ? 'active' : ''}"
               id="tab-${server.name}"
               href="#"
               onclick="selectServer('${server.name}'); return false;">
                <span class="server-status ${server.connected ? 'online' : (server.online ? 'connecting' : 'offline')}"></span>
                ${server.name}
            </a>
        </li>
    `).join('');

    // Build content panels with htmx attributes
    contentContainer.innerHTML = servers.map((server, index) => `
        <div class="tab-pane ${index === 0 ? 'show active' : ''}" id="panel-${server.name}">
            <div class="d-flex justify-content-between align-items-center my-2">
                <ul class="nav nav-pills" id="subtabs-${server.name}">
                    <li class="nav-item">
                        <a class="nav-link active" href="#" onclick="showSubTab('${server.name}', 'live'); return false;">Live</a>
                    </li>
                    <li class="nav-item">
                        <a class="nav-link" href="#" onclick="showSubTab('${server.name}', 'history'); return false;">History</a>
                    </li>
                    <li class="nav-item">
                        <a class="nav-link" href="#" onclick="showSubTab('${server.name}', 'analytics'); return false;">Analytics</a>
                    </li>
                </ul>
                <div>
                    <span id="status-${server.name}" class="badge ${server.connected ? 'bg-success' : 'bg-danger'} me-2">
                        ${server.connected ? 'Connected' : 'Disconnected'}
                    </span>
                    <button class="btn btn-outline-info btn-sm me-1" onclick="copySelection('${server.name}')">Copy Selection</button>
                    <button class="btn btn-outline-secondary btn-sm" onclick="clearServerLogs('${server.name}')">Clear Logs</button>
                </div>
            </div>
            <div id="live-${server.name}" class="subtab-content">
                <div id="terminal-${server.name}" class="terminal-container"></div>
            </div>
            <div id="history-${server.name}" class="subtab-content" style="display: none;">
                <div class="row">
                    <div class="col-md-2">
                        <div class="list-group log-list" id="loglist-${server.name}"
                             hx-get="/htmx/servers/${server.name}/logs"
                             hx-trigger="loadLogs, refreshLogs">
                            <div class="list-group-item text-muted small">Select History tab to load...</div>
                        </div>
                    </div>
                    <div class="col-md-10">
                        <div class="log-viewer" id="log-content-${server.name}">
                            <div class="text-muted p-3">Select a log file to view...</div>
                        </div>
                    </div>
                </div>
            </div>
            <div id="analytics-${server.name}" class="subtab-content" style="display: none;">
                <div class="analytics-panel" id="analytics-content-${server.name}"
                     hx-get="/htmx/servers/${server.name}/analytics"
                     hx-trigger="loadAnalytics">
                    <div class="text-muted">Select Analytics tab to load...</div>
                </div>
            </div>
        </div>
    `).join('');

    // Initialize all server sessions
    servers.forEach(server => {
        initServerSession(server.name);
    });

    // Process htmx on new content
    htmx.process(contentContainer);

    // Apply URL hash state or select first server
    const state = parseHash();
    if (state && servers.some(s => s.name === state.server)) {
        currentServer = state.server;
        selectServer(state.server);
        showSubTab(state.server, state.tab);
    } else if (servers.length > 0) {
        currentServer = servers[0].name;
    }
}

function updateServerStatus() {
    servers.forEach(server => {
        // Update tab status indicator
        const tab = document.getElementById(`tab-${server.name}`);
        if (tab) {
            const statusSpan = tab.querySelector('.server-status');
            if (statusSpan) {
                statusSpan.className = `server-status ${server.connected ? 'online' : (server.online ? 'connecting' : 'offline')}`;
            }
        }

        // Update badge
        const badge = document.getElementById(`status-${server.name}`);
        if (badge) {
            badge.className = `badge ${server.connected ? 'bg-success' : 'bg-danger'} me-2`;
            badge.textContent = server.connected ? 'Connected' : 'Disconnected';
        }
    });
}

function createTerminal() {
    const term = new Terminal({
        cursorBlink: false,
        cursorStyle: 'block',
        fontSize: 14,
        fontFamily: 'Menlo, Monaco, "Courier New", monospace',
        theme: {
            background: '#0a0a0a',
            foreground: '#00ff00',
            cursor: '#00ff00',
            cursorAccent: '#0a0a0a',
            selectionBackground: '#44aa44',
            selectionForeground: '#000000',
            black: '#000000',
            red: '#ff0000',
            green: '#00ff00',
            yellow: '#ffff00',
            blue: '#0000ff',
            magenta: '#ff00ff',
            cyan: '#00ffff',
            white: '#ffffff',
            brightBlack: '#808080',
            brightRed: '#ff0000',
            brightGreen: '#00ff00',
            brightYellow: '#ffff00',
            brightBlue: '#0000ff',
            brightMagenta: '#ff00ff',
            brightCyan: '#00ffff',
            brightWhite: '#ffffff'
        },
        scrollback: 10000,
        convertEol: true,
        allowProposedApi: true
    });

    const fit = new FitAddon.FitAddon();
    term.loadAddon(fit);

    // Enable right-click to copy selection
    term.attachCustomKeyEventHandler((event) => {
        // Ctrl+C or Cmd+C when there's a selection = copy
        if ((event.ctrlKey || event.metaKey) && event.key === 'c' && term.hasSelection()) {
            navigator.clipboard.writeText(term.getSelection());
            return false; // Prevent default
        }
        return true;
    });

    return { term, fit };
}

function initServerSession(name) {
    const container = document.getElementById(`terminal-${name}`);
    if (!container) return;

    // Create live terminal
    const { term, fit } = createTerminal();
    term.open(container);

    serverSessions[name] = {
        terminal: term,
        fitAddon: fit,
        eventSource: null,
        currentLogFile: null,
        lastLogCount: 0
    };

    // Fit after a short delay
    setTimeout(() => {
        fit.fit();
    }, 100);

    // Start streaming
    startServerStream(name);

    // Start checking for new log files
    checkForNewLogs(name);
}

function startServerStream(name) {
    const session = serverSessions[name];
    if (!session) return;

    if (session.eventSource) {
        session.eventSource.close();
    }

    const eventSource = new EventSource(`/api/servers/${encodeURIComponent(name)}/stream`);

    eventSource.addEventListener('connected', (event) => {
        console.log('SSE connected to:', event.data);
    });

    eventSource.onmessage = (event) => {
        const decoded = atob(event.data);
        // Check for clear screen sequences (BIOS often sends these)
        // \x1b[2J = clear screen, \x1b[H or \x1b[;H = cursor home
        if (decoded.includes('\x1b[2J') || decoded.includes('\x1b[;H') || decoded.includes('\x1b[1;1H')) {
            session.terminal.clear();
        }
        session.terminal.write(decoded);
    };

    eventSource.onerror = (error) => {
        console.error('SSE error for', name, ':', error);
        if (eventSource.readyState === EventSource.CLOSED) {
            setTimeout(() => startServerStream(name), 3000);
        }
    };

    session.eventSource = eventSource;
}

// Check for new log files and auto-switch when a new boot happens
async function checkForNewLogs(serverName) {
    const session = serverSessions[serverName];
    if (!session) return;

    try {
        const response = await fetch(`/api/servers/${encodeURIComponent(serverName)}/logs`);
        const logs = await response.json();

        if (logs && logs.length > 0) {
            // If log count increased, there's a new log file (new boot)
            if (session.lastLogCount > 0 && logs.length > session.lastLogCount) {
                console.log(`New log file detected for ${serverName}, refreshing list`);
                // Trigger htmx refresh of log list (the new list will auto-load the first log)
                htmx.trigger(`#loglist-${serverName}`, 'refreshLogs');
                // Also refresh analytics to show new boot
                htmx.trigger(`#analytics-content-${serverName}`, 'loadAnalytics');
            }
            session.lastLogCount = logs.length;
        }
    } catch (error) {
        console.error('Failed to check logs:', error);
    }

    // Check again in 5 seconds
    setTimeout(() => checkForNewLogs(serverName), 5000);
}

function selectServer(name) {
    currentServer = name;

    // Update tab active states
    document.querySelectorAll('#server-tabs .nav-link').forEach(tab => {
        tab.classList.remove('active');
    });
    document.getElementById(`tab-${name}`).classList.add('active');

    // Update panel visibility
    document.querySelectorAll('#server-content .tab-pane').forEach(panel => {
        panel.classList.remove('show', 'active');
    });
    document.getElementById(`panel-${name}`).classList.add('show', 'active');

    // Refit the terminal
    const session = serverSessions[name];
    if (session && session.fitAddon) {
        setTimeout(() => session.fitAddon.fit(), 50);
    }
}

function showSubTab(serverName, tab) {
    const subtabs = document.querySelectorAll(`#subtabs-${serverName} .nav-link`);
    subtabs.forEach(t => t.classList.remove('active'));

    const livePanel = document.getElementById(`live-${serverName}`);
    const historyPanel = document.getElementById(`history-${serverName}`);
    const analyticsPanel = document.getElementById(`analytics-${serverName}`);

    livePanel.style.display = 'none';
    historyPanel.style.display = 'none';
    analyticsPanel.style.display = 'none';

    const session = serverSessions[serverName];

    if (tab === 'live') {
        subtabs[0].classList.add('active');
        livePanel.style.display = 'block';

        if (session && session.fitAddon) {
            setTimeout(() => session.fitAddon.fit(), 10);
        }
    } else if (tab === 'history') {
        subtabs[1].classList.add('active');
        historyPanel.style.display = 'block';
        // Trigger htmx to load the log list
        htmx.trigger(`#loglist-${serverName}`, 'loadLogs');
    } else if (tab === 'analytics') {
        subtabs[2].classList.add('active');
        analyticsPanel.style.display = 'block';
        // Trigger htmx to load analytics
        htmx.trigger(`#analytics-content-${serverName}`, 'loadAnalytics');
    }
}

// Called by htmx before loading a log file
function setActiveLog(element) {
    const list = element.closest('.log-list');
    if (list) {
        list.querySelectorAll('.list-group-item').forEach(item => {
            item.classList.remove('active');
        });
        element.classList.add('active');
    }
}

async function clearServerLogs(serverName) {
    if (!confirm(`Clear all logs for ${serverName}?`)) return;

    try {
        await fetch(`/api/servers/${encodeURIComponent(serverName)}/logs/clear`, { method: 'POST' });
        // Trigger htmx refresh
        htmx.trigger(`#loglist-${serverName}`, 'refreshLogs');
        // Clear log content
        document.getElementById(`log-content-${serverName}`).innerHTML =
            '<div class="text-muted p-3">Select a log file to view...</div>';
    } catch (error) {
        console.error('Failed to clear logs:', error);
    }
}

async function clearAllLogs() {
    if (!confirm('Clear ALL logs for ALL servers?')) return;

    try {
        await fetch('/api/logs/clear', { method: 'POST' });
        // Trigger refresh on all log lists
        servers.forEach(server => {
            htmx.trigger(`#loglist-${server.name}`, 'refreshLogs');
            document.getElementById(`log-content-${server.name}`).innerHTML =
                '<div class="text-muted p-3">Select a log file to view...</div>';
        });
    } catch (error) {
        console.error('Failed to clear all logs:', error);
    }
}

function copySelection(serverName) {
    const session = serverSessions[serverName];
    if (!session) return;

    // Check which panel is active
    const livePanel = document.getElementById(`live-${serverName}`);
    const historyPanel = document.getElementById(`history-${serverName}`);

    if (livePanel && livePanel.style.display !== 'none') {
        // Copy from terminal
        const selection = session.terminal.getSelection();
        if (selection) {
            navigator.clipboard.writeText(selection).then(() => {
                showCopyFeedback(event.target);
            }).catch(err => {
                console.error('Failed to copy:', err);
            });
        } else {
            alert('No text selected. Click and drag to select text in the terminal.');
        }
    } else if (historyPanel && historyPanel.style.display !== 'none') {
        // Copy from log viewer (regular text selection)
        const selection = window.getSelection().toString();
        if (selection) {
            navigator.clipboard.writeText(selection).then(() => {
                showCopyFeedback(event.target);
            }).catch(err => {
                console.error('Failed to copy:', err);
            });
        } else {
            alert('No text selected. Click and drag to select text.');
        }
    }
}

function showCopyFeedback(btn) {
    const originalText = btn.textContent;
    btn.textContent = 'Copied!';
    setTimeout(() => btn.textContent = originalText, 1000);
}

// Handle window resize
window.addEventListener('resize', () => {
    Object.values(serverSessions).forEach(session => {
        if (session.fitAddon) session.fitAddon.fit();
    });
});

// Listen for hash changes
window.addEventListener('hashchange', () => {
    const state = parseHash();
    if (state && servers.some(s => s.name === state.server)) {
        selectServer(state.server);
        showSubTab(state.server, state.tab);
    }
});

// Wrap selectServer to update hash
const originalSelectServer = selectServer;
selectServer = function(name) {
    originalSelectServer(name);
    updateHash(name, 'live');
};

// Wrap showSubTab to update hash
const originalShowSubTab = showSubTab;
showSubTab = function(serverName, tab) {
    originalShowSubTab(serverName, tab);
    updateHash(serverName, tab);
};

// Initial load and periodic refresh
fetchServers();
setInterval(fetchServers, 10000);
