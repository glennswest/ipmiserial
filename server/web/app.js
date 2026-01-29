let currentServer = null;
let servers = [];
let historyTerminal = null;
let historyFitAddon = null;

// Per-server sessions: { name: { terminal, fitAddon, eventSource, container } }
const serverSessions = {};

async function fetchServers() {
    try {
        const response = await fetch('/api/servers');
        servers = await response.json();
        renderServerList();
        document.getElementById('server-count').textContent = `${servers.length} servers`;

        // Start streams for all servers in background
        servers.forEach(server => {
            if (!serverSessions[server.name]) {
                initServerSession(server.name);
            }
        });
    } catch (error) {
        console.error('Failed to fetch servers:', error);
    }
}

function renderServerList() {
    const list = document.getElementById('server-list');

    if (servers.length === 0) {
        list.innerHTML = '<div class="list-group-item text-muted">No servers found</div>';
        return;
    }

    list.innerHTML = servers.map(server => `
        <a href="#" class="list-group-item list-group-item-action ${currentServer === server.name ? 'active' : ''}"
           onclick="selectServer('${server.name}'); return false;">
            <span class="server-status ${server.connected ? 'online' : (server.online ? 'connecting' : 'offline')}"></span>
            ${server.name}
        </a>
    `).join('');
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
        convertEol: true
    });

    const fit = new FitAddon.FitAddon();
    term.loadAddon(fit);

    return { term, fit };
}

function initServerSession(name) {
    const mainContainer = document.getElementById('terminal-container');

    // Create a container div for this server's terminal
    const container = document.createElement('div');
    container.id = `terminal-${name}`;
    container.style.display = 'none';
    container.style.width = '100%';
    container.style.height = '100%';
    mainContainer.appendChild(container);

    // Create terminal
    const { term, fit } = createTerminal();
    term.open(container);

    serverSessions[name] = {
        terminal: term,
        fitAddon: fit,
        eventSource: null,
        container: container
    };

    // Start streaming with auto-reconnect
    startServerStream(name);
}

function startServerStream(name) {
    const session = serverSessions[name];
    if (!session) return;

    // Close existing connection if any
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
        // Auto-reconnect after 3 seconds
        if (eventSource.readyState === EventSource.CLOSED) {
            setTimeout(() => startServerStream(name), 3000);
        }
    };

    session.eventSource = eventSource;
}

function selectServer(name) {
    currentServer = name;
    renderServerList();

    document.getElementById('no-server-selected').style.display = 'none';
    document.getElementById('server-panel').style.display = 'block';
    document.getElementById('server-name').textContent = name;

    const server = servers.find(s => s.name === name);
    updateConnectionStatus(server);

    // Hide all terminal containers
    Object.values(serverSessions).forEach(session => {
        session.container.style.display = 'none';
    });

    // Show selected server's terminal
    if (serverSessions[name]) {
        serverSessions[name].container.style.display = 'block';
        setTimeout(() => {
            serverSessions[name].fitAddon.fit();
        }, 10);
    }

    // Show live tab by default
    showTab('live');
}

function updateConnectionStatus(server) {
    const badge = document.getElementById('connection-status');
    if (server.connected) {
        badge.className = 'badge bg-success';
        badge.textContent = 'Connected';
    } else if (server.online) {
        badge.className = 'badge bg-warning';
        badge.textContent = 'Connecting...';
    } else {
        badge.className = 'badge bg-danger';
        badge.textContent = 'Offline';
    }
}

function showTab(tab) {
    document.querySelectorAll('.nav-link').forEach(link => {
        link.classList.toggle('active', link.dataset.tab === tab);
    });

    document.getElementById('tab-live').style.display = tab === 'live' ? 'block' : 'none';
    document.getElementById('tab-history').style.display = tab === 'history' ? 'block' : 'none';

    if (tab === 'history') {
        loadLogList();
    }

    // Refit terminals when tab changes
    setTimeout(() => {
        if (tab === 'live' && currentServer && serverSessions[currentServer]) {
            serverSessions[currentServer].fitAddon.fit();
        } else if (tab === 'history' && historyFitAddon) {
            historyFitAddon.fit();
        }
    }, 100);
}

function initHistoryTerminal(containerId) {
    const container = document.getElementById(containerId);
    container.innerHTML = '';

    const { term, fit } = createTerminal();
    term.open(container);
    fit.fit();

    return { term, fit };
}

async function loadLogList() {
    if (!currentServer) return;

    try {
        const response = await fetch(`/api/servers/${encodeURIComponent(currentServer)}/logs`);
        const logs = await response.json();

        const list = document.getElementById('log-list');

        if (!logs || logs.length === 0) {
            list.innerHTML = '<div class="list-group-item text-muted">No logs available</div>';
            return;
        }

        list.innerHTML = logs.map(log => `
            <a href="#" class="list-group-item list-group-item-action"
               onclick="loadLog('${log}'); return false;">
                ${log}
            </a>
        `).join('');
    } catch (error) {
        console.error('Failed to load log list:', error);
    }
}

async function loadLog(filename) {
    if (!currentServer) return;

    try {
        const response = await fetch(`/api/servers/${encodeURIComponent(currentServer)}/logs/${encodeURIComponent(filename)}`);
        const content = await response.text();

        // Initialize history terminal if needed
        if (!historyTerminal) {
            const result = initHistoryTerminal('history-terminal-container');
            historyTerminal = result.term;
            historyFitAddon = result.fit;
        } else {
            historyTerminal.clear();
        }

        historyTerminal.write(content);
    } catch (error) {
        console.error('Failed to load log:', error);
    }
}

// Tab click handlers
document.querySelectorAll('.nav-link').forEach(link => {
    link.addEventListener('click', (e) => {
        e.preventDefault();
        showTab(link.dataset.tab);
    });
});

// Handle window resize
window.addEventListener('resize', () => {
    Object.values(serverSessions).forEach(session => {
        if (session.container.style.display !== 'none') {
            session.fitAddon.fit();
        }
    });
    if (historyFitAddon) historyFitAddon.fit();
});

// Initial load and periodic refresh
fetchServers();
setInterval(fetchServers, 10000);
