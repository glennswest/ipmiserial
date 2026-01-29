let servers = [];
let currentServer = null;

// Per-server sessions: { name: { terminal, fitAddon, eventSource, historyTerminal, historyFitAddon } }
const serverSessions = {};

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

    // Build content panels
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
                        <div class="list-group log-list" id="loglist-${server.name}"></div>
                    </div>
                    <div class="col-md-10">
                        <div id="history-terminal-${server.name}" class="terminal-container"></div>
                    </div>
                </div>
            </div>
        </div>
    `).join('');

    // Initialize all server sessions
    servers.forEach(server => {
        initServerSession(server.name);
    });

    // Select first server
    if (servers.length > 0) {
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
        historyTerminal: null,
        historyFitAddon: null
    };

    // Fit after a short delay
    setTimeout(() => fit.fit(), 100);

    // Start streaming
    startServerStream(name);
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

    if (tab === 'live') {
        subtabs[0].classList.add('active');
        livePanel.style.display = 'block';
        historyPanel.style.display = 'none';

        const session = serverSessions[serverName];
        if (session && session.fitAddon) {
            setTimeout(() => session.fitAddon.fit(), 50);
        }
    } else {
        subtabs[1].classList.add('active');
        livePanel.style.display = 'none';
        historyPanel.style.display = 'block';

        loadLogList(serverName);
    }
}

async function loadLogList(serverName) {
    try {
        const response = await fetch(`/api/servers/${encodeURIComponent(serverName)}/logs`);
        const logs = await response.json();

        const list = document.getElementById(`loglist-${serverName}`);

        if (!logs || logs.length === 0) {
            list.innerHTML = '<div class="list-group-item text-muted small">No logs</div>';
            return;
        }

        const session = serverSessions[serverName];
        const currentLog = session ? session.currentLogFile : null;

        list.innerHTML = logs.map(log => `
            <a href="#" class="list-group-item list-group-item-action small ${currentLog === log ? 'active' : ''}"
               id="logitem-${serverName}-${log.replace(/[^a-zA-Z0-9]/g, '_')}"
               onclick="loadLog('${serverName}', '${log}'); return false;">
                ${log}
            </a>
        `).join('');

        // Initialize history terminal early so it's ready
        if (session && !session.historyTerminal) {
            const container = document.getElementById(`history-terminal-${serverName}`);
            const { term, fit } = createTerminal();
            term.open(container);
            session.historyTerminal = term;
            session.historyFitAddon = fit;
            setTimeout(() => fit.fit(), 50);
        }
    } catch (error) {
        console.error('Failed to load log list:', error);
    }
}

async function loadLog(serverName, filename) {
    const session = serverSessions[serverName];
    if (!session) return;

    // Update active state in list immediately
    const list = document.getElementById(`loglist-${serverName}`);
    list.querySelectorAll('.list-group-item').forEach(item => {
        item.classList.remove('active');
    });
    const itemId = `logitem-${serverName}-${filename.replace(/[^a-zA-Z0-9]/g, '_')}`;
    const activeItem = document.getElementById(itemId);
    if (activeItem) {
        activeItem.classList.add('active');
    }

    // Store current log file
    session.currentLogFile = filename;

    // Initialize history terminal if needed
    if (!session.historyTerminal) {
        const container = document.getElementById(`history-terminal-${serverName}`);
        const { term, fit } = createTerminal();
        term.open(container);
        session.historyTerminal = term;
        session.historyFitAddon = fit;
        setTimeout(() => fit.fit(), 50);
    }

    // Clear and show loading indicator
    session.historyTerminal.clear();
    session.historyTerminal.write('\x1b[33mLoading...\x1b[0m');

    try {
        const response = await fetch(`/api/servers/${encodeURIComponent(serverName)}/logs/${encodeURIComponent(filename)}`);
        const content = await response.text();

        session.historyTerminal.clear();
        session.historyTerminal.write(content);
    } catch (error) {
        console.error('Failed to load log:', error);
        session.historyTerminal.clear();
        session.historyTerminal.write('\x1b[31mError loading log\x1b[0m');
    }
}

async function clearServerLogs(serverName) {
    if (!confirm(`Clear all logs for ${serverName}?`)) return;

    try {
        await fetch(`/api/servers/${encodeURIComponent(serverName)}/logs/clear`, { method: 'POST' });

        // Refresh log list if viewing history
        const historyPanel = document.getElementById(`history-${serverName}`);
        if (historyPanel && historyPanel.style.display !== 'none') {
            loadLogList(serverName);
        }
    } catch (error) {
        console.error('Failed to clear logs:', error);
    }
}

async function clearAllLogs() {
    if (!confirm('Clear ALL logs for ALL servers?')) return;

    try {
        await fetch('/api/logs/clear', { method: 'POST' });

        // Refresh any visible log lists
        servers.forEach(server => {
            const historyPanel = document.getElementById(`history-${server.name}`);
            if (historyPanel && historyPanel.style.display !== 'none') {
                loadLogList(server.name);
            }
        });
    } catch (error) {
        console.error('Failed to clear all logs:', error);
    }
}

function copySelection(serverName) {
    const session = serverSessions[serverName];
    if (!session) return;

    // Check which terminal is active (live or history)
    const livePanel = document.getElementById(`live-${serverName}`);
    const term = (livePanel && livePanel.style.display !== 'none')
        ? session.terminal
        : session.historyTerminal;

    if (!term) return;

    const selection = term.getSelection();
    if (selection) {
        navigator.clipboard.writeText(selection).then(() => {
            // Brief visual feedback
            const btn = event.target;
            const originalText = btn.textContent;
            btn.textContent = 'Copied!';
            setTimeout(() => btn.textContent = originalText, 1000);
        }).catch(err => {
            console.error('Failed to copy:', err);
        });
    } else {
        alert('No text selected. Click and drag to select text in the terminal.');
    }
}

// Handle window resize
window.addEventListener('resize', () => {
    Object.values(serverSessions).forEach(session => {
        if (session.fitAddon) session.fitAddon.fit();
        if (session.historyFitAddon) session.historyFitAddon.fit();
    });
});

// Initial load and periodic refresh
fetchServers();
setInterval(fetchServers, 10000);
