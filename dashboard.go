package main

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"time"
)

func (s *server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(dashboardHTML))
}

func (s *server) handleDashboardLogs(w http.ResponseWriter, r *http.Request) {
	tuiLogsMu.Lock()
	defer tuiLogsMu.Unlock()

	// Prepare JSON payload
	data := map[string]any{
		"uptime": time.Since(startTime).Round(time.Second).String(),
		"logs":   tuiLogs,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (s *server) handleDashboardClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	tuiLogsMu.Lock()
	tuiLogs = nil
	tuiLogsMu.Unlock()
	w.WriteHeader(200)
}

func (s *server) handleDashboardRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	w.WriteHeader(200)
	go func() {
		time.Sleep(500 * time.Millisecond)
		exec.Command("acc-restart").Run()
	}()
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>▲ ACC Proxy Gateway Dashboard</title>
  <meta name="description" content="Interactive dashboard for controlling and monitoring the ACC proxy gateway">
  <link href="https://fonts.googleapis.com/css2?family=Inter:wght@300;400;500;600;700&display=swap" rel="stylesheet">
  <script src="https://cdn.jsdelivr.net/npm/lucide@0.344.0/dist/umd/lucide.min.js"></script>
  <style>
    :root {
      --bg-gradient-start: #060913;
      --bg-gradient-end: #0f132a;
      --panel-bg: rgba(17, 24, 39, 0.55);
      --panel-border: rgba(255, 255, 255, 0.06);
      --panel-border-hover: rgba(6, 182, 212, 0.2);
      
      --text-primary: #f3f4f6;
      --text-secondary: #9ca3af;
      --text-muted: #6b7280;
      
      --cyan: #06b6d4;
      --cyan-glow: rgba(6, 182, 212, 0.4);
      --green: #10b981;
      --green-glow: rgba(16, 185, 129, 0.4);
      --yellow: #f59e0b;
      --yellow-glow: rgba(245, 158, 11, 0.4);
      --purple: #8b5cf6;
      --purple-glow: rgba(139, 92, 246, 0.4);
      --red: #ef4444;
      --red-glow: rgba(239, 68, 68, 0.4);
    }

    * {
      box-sizing: border-box;
      margin: 0;
      padding: 0;
    }

    body {
      font-family: 'Inter', -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      background: radial-gradient(circle at 50% 0%, var(--bg-gradient-start) 0%, var(--bg-gradient-end) 100%);
      color: var(--text-primary);
      min-height: 100vh;
      overflow-x: hidden;
      padding: 2.5rem 2rem;
    }

    .container {
      max-width: 1200px;
      margin: 0 auto;
    }

    /* Header */
    header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 2.5rem;
      animation: fadeInDown 0.6s cubic-bezier(0.16, 1, 0.3, 1);
    }

    .brand {
      display: flex;
      align-items: center;
      gap: 1rem;
    }

    .logo-icon {
      color: var(--cyan);
      filter: drop-shadow(0 0 10px var(--cyan-glow));
    }

    .brand h1 {
      font-size: 1.6rem;
      font-weight: 700;
      letter-spacing: -0.025em;
      background: linear-gradient(135deg, #fff 0%, var(--text-secondary) 100%);
      -webkit-background-clip: text;
      -webkit-text-fill-color: transparent;
    }

    .brand p {
      font-size: 0.85rem;
      color: var(--text-muted);
      margin-top: 0.15rem;
    }

    /* Actions */
    .header-actions {
      display: flex;
      gap: 1rem;
    }

    .btn {
      display: inline-flex;
      align-items: center;
      gap: 0.5rem;
      padding: 0.65rem 1.25rem;
      font-size: 0.875rem;
      font-weight: 500;
      border-radius: 8px;
      cursor: pointer;
      transition: all 0.3s cubic-bezier(0.16, 1, 0.3, 1);
      border: 1px solid transparent;
    }

    .btn-clear {
      background: rgba(255, 255, 255, 0.03);
      border-color: rgba(255, 255, 255, 0.05);
      color: var(--text-secondary);
    }

    .btn-clear:hover {
      background: rgba(255, 255, 255, 0.08);
      border-color: rgba(255, 255, 255, 0.15);
      color: var(--text-primary);
    }

    .btn-restart {
      background: rgba(239, 68, 68, 0.1);
      border-color: rgba(239, 68, 68, 0.2);
      color: #fca5a5;
    }

    .btn-restart:hover {
      background: rgba(239, 68, 68, 0.2);
      border-color: rgba(239, 68, 68, 0.4);
      color: #fee2e2;
      box-shadow: 0 0 15px rgba(239, 68, 68, 0.15);
    }

    /* Status Grid */
    .stats-grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
      gap: 1.5rem;
      margin-bottom: 2rem;
      animation: fadeInUp 0.6s cubic-bezier(0.16, 1, 0.3, 1) 0.1s both;
    }

    .card {
      background: var(--panel-bg);
      backdrop-filter: blur(16px);
      -webkit-backdrop-filter: blur(16px);
      border: 1px solid var(--panel-border);
      border-radius: 12px;
      padding: 1.5rem;
      display: flex;
      align-items: center;
      gap: 1.25rem;
      transition: border-color 0.3s ease, box-shadow 0.3s ease;
    }

    .card:hover {
      border-color: var(--panel-border-hover);
      box-shadow: 0 8px 30px rgba(0, 0, 0, 0.2);
    }

    .card-icon {
      display: flex;
      align-items: center;
      justify-content: center;
      width: 48px;
      height: 48px;
      border-radius: 10px;
      background: rgba(255, 255, 255, 0.02);
      border: 1px solid rgba(255, 255, 255, 0.04);
    }

    .card-icon.online {
      color: var(--green);
      background: rgba(16, 185, 129, 0.05);
      border-color: rgba(16, 185, 129, 0.1);
      filter: drop-shadow(0 0 4px var(--green-glow));
    }

    .card-icon.port {
      color: var(--cyan);
      background: rgba(6, 182, 212, 0.05);
      border-color: rgba(6, 182, 212, 0.1);
    }

    .card-icon.uptime {
      color: var(--purple);
      background: rgba(139, 92, 246, 0.05);
      border-color: rgba(139, 92, 246, 0.1);
    }

    .card-content h3 {
      font-size: 0.8rem;
      text-transform: uppercase;
      letter-spacing: 0.05em;
      color: var(--text-muted);
      font-weight: 500;
    }

    .card-content p {
      font-size: 1.25rem;
      font-weight: 600;
      color: var(--text-primary);
      margin-top: 0.25rem;
    }

    /* Model routes section */
    .section-title {
      font-size: 1rem;
      font-weight: 600;
      text-transform: uppercase;
      letter-spacing: 0.05em;
      color: var(--text-secondary);
      margin-bottom: 1rem;
      display: flex;
      align-items: center;
      gap: 0.5rem;
    }

    .routes-panel {
      background: var(--panel-bg);
      backdrop-filter: blur(16px);
      -webkit-backdrop-filter: blur(16px);
      border: 1px solid var(--panel-border);
      border-radius: 14px;
      padding: 1.75rem;
      margin-bottom: 2.5rem;
      animation: fadeInUp 0.6s cubic-bezier(0.16, 1, 0.3, 1) 0.2s both;
    }

    .routes-grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
      gap: 1.25rem;
    }

    .route-card {
      background: rgba(255, 255, 255, 0.01);
      border: 1px solid rgba(255, 255, 255, 0.03);
      border-radius: 10px;
      padding: 1.25rem;
      display: flex;
      flex-direction: column;
      gap: 0.75rem;
      transition: all 0.3s ease;
    }

    .route-card:hover {
      background: rgba(255, 255, 255, 0.02);
      border-color: rgba(255, 255, 255, 0.08);
      transform: translateY(-2px);
    }

    .route-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
    }

    .route-badge {
      font-size: 0.75rem;
      font-weight: 600;
      padding: 0.2rem 0.5rem;
      border-radius: 4px;
      text-transform: uppercase;
      background: rgba(255, 255, 255, 0.05);
      border: 1px solid rgba(255, 255, 255, 0.08);
    }

    .route-badge.opencode {
      color: #38bdf8;
      background: rgba(56, 189, 248, 0.08);
      border-color: rgba(56, 189, 248, 0.2);
    }

    .route-badge.nvidia {
      color: #4ade80;
      background: rgba(74, 222, 128, 0.08);
      border-color: rgba(74, 222, 128, 0.2);
    }

    .route-source {
      font-size: 0.9rem;
      font-weight: 500;
      color: var(--text-primary);
    }

    .route-arrow {
      color: var(--text-muted);
      font-size: 0.8rem;
    }

    .route-dest {
      display: flex;
      align-items: center;
      gap: 0.4rem;
      font-size: 0.85rem;
      color: var(--text-secondary);
    }

    .route-dest-icon {
      color: var(--cyan);
    }

    .effort-badge {
      display: inline-flex;
      align-items: center;
      gap: 0.25rem;
      font-size: 0.7rem;
      color: #fef08a;
      background: rgba(254, 240, 138, 0.08);
      border: 1px solid rgba(254, 240, 138, 0.15);
      padding: 0.15rem 0.4rem;
      border-radius: 4px;
      font-weight: 500;
      align-self: flex-start;
    }

    /* Logs Panel */
    .logs-panel {
      background: var(--panel-bg);
      backdrop-filter: blur(16px);
      -webkit-backdrop-filter: blur(16px);
      border: 1px solid var(--panel-border);
      border-radius: 14px;
      padding: 1.75rem;
      animation: fadeInUp 0.6s cubic-bezier(0.16, 1, 0.3, 1) 0.3s both;
    }

    .logs-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 1.25rem;
    }

    .logs-container {
      overflow-x: auto;
      border-radius: 10px;
      border: 1px solid rgba(255, 255, 255, 0.03);
    }

    table {
      width: 100%;
      border-collapse: collapse;
      text-align: left;
      font-size: 0.875rem;
    }

    th {
      background: rgba(255, 255, 255, 0.015);
      color: var(--text-secondary);
      font-weight: 500;
      padding: 1rem 1.25rem;
      border-bottom: 1px solid rgba(255, 255, 255, 0.05);
      font-size: 0.8rem;
      text-transform: uppercase;
      letter-spacing: 0.05em;
    }

    td {
      padding: 1rem 1.25rem;
      border-bottom: 1px solid rgba(255, 255, 255, 0.03);
      color: var(--text-primary);
      vertical-align: middle;
    }

    tr:last-child td {
      border-bottom: none;
    }

    tr:hover td {
      background: rgba(255, 255, 255, 0.005);
    }

    .time-col {
      color: var(--text-muted);
      font-family: monospace;
      font-size: 0.8rem;
    }

    .model-name {
      font-weight: 600;
      color: #fff;
    }

    .route-target {
      color: var(--yellow);
      font-family: monospace;
      font-size: 0.85rem;
    }

    .status-badge {
      display: inline-flex;
      align-items: center;
      gap: 0.25rem;
      font-weight: 600;
      font-size: 0.75rem;
      padding: 0.25rem 0.6rem;
      border-radius: 6px;
      text-transform: uppercase;
    }

    .status-badge.ok {
      color: #a7f3d0;
      background: rgba(16, 185, 129, 0.1);
      border: 1px solid rgba(16, 185, 129, 0.2);
    }

    .status-badge.err {
      color: #fca5a5;
      background: rgba(239, 68, 68, 0.1);
      border: 1px solid rgba(239, 68, 68, 0.2);
    }

    .tokens-pill {
      font-size: 0.75rem;
      color: var(--text-secondary);
      background: rgba(255, 255, 255, 0.04);
      padding: 0.2rem 0.5rem;
      border-radius: 4px;
      font-family: monospace;
    }

    .no-logs {
      text-align: center;
      padding: 3rem 0;
      color: var(--text-muted);
    }

    .no-logs p {
      margin-top: 0.5rem;
      font-size: 0.9rem;
    }

    /* Modal / Overlay for restart */
    .overlay {
      position: fixed;
      top: 0;
      left: 0;
      right: 0;
      bottom: 0;
      background: rgba(3, 7, 18, 0.85);
      backdrop-filter: blur(12px);
      -webkit-backdrop-filter: blur(12px);
      z-index: 1000;
      display: flex;
      align-items: center;
      justify-content: center;
      opacity: 0;
      pointer-events: none;
      transition: opacity 0.3s ease;
    }

    .overlay.active {
      opacity: 1;
      pointer-events: auto;
    }

    .modal {
      background: rgba(17, 24, 39, 0.9);
      border: 1px solid rgba(255, 255, 255, 0.08);
      border-radius: 16px;
      padding: 2.5rem;
      width: 400px;
      text-align: center;
      box-shadow: 0 20px 50px rgba(0, 0, 0, 0.5);
      transform: scale(0.95);
      transition: transform 0.3s cubic-bezier(0.16, 1, 0.3, 1);
    }

    .overlay.active .modal {
      transform: scale(1);
    }

    .spinner {
      border: 3px solid rgba(255, 255, 255, 0.05);
      border-top: 3px solid var(--cyan);
      border-radius: 50%;
      width: 40px;
      height: 40px;
      animation: spin 1s linear infinite;
      margin: 0 auto 1.5rem auto;
      filter: drop-shadow(0 0 8px var(--cyan-glow));
    }

    .modal h2 {
      font-size: 1.25rem;
      margin-bottom: 0.5rem;
    }

    .modal p {
      color: var(--text-secondary);
      font-size: 0.9rem;
    }

    /* Animations */
    @keyframes spin {
      0% { transform: rotate(0deg); }
      100% { transform: rotate(360deg); }
    }

    @keyframes fadeInDown {
      from { opacity: 0; transform: translateY(-10px); }
      to { opacity: 1; transform: translateY(0); }
    }

    @keyframes fadeInUp {
      from { opacity: 0; transform: translateY(15px); }
      to { opacity: 1; transform: translateY(0); }
    }

    @keyframes rowSlideIn {
      from { opacity: 0; transform: translateX(-5px); }
      to { opacity: 1; transform: translateX(0); }
    }

    .new-row {
      animation: rowSlideIn 0.4s cubic-bezier(0.16, 1, 0.3, 1) both;
    }
  </style>
</head>
<body>
  <div class="container">
    <header>
      <div class="brand">
        <i data-lucide="triangle" class="logo-icon" size="28"></i>
        <div>
          <h1>ACC PROXY GATEWAY</h1>
          <p>Local Intelligent Router & Translator</p>
        </div>
      </div>
      <div class="header-actions">
        <button id="clearBtn" class="btn btn-clear">
          <i data-lucide="trash-2" size="16"></i>
          Clear Logs
        </button>
        <button id="restartBtn" class="btn btn-restart">
          <i data-lucide="refresh-cw" size="16"></i>
          Restart Proxy
        </button>
      </div>
    </header>

    <div class="stats-grid">
      <div class="card">
        <div class="card-icon online">
          <i data-lucide="activity" size="20"></i>
        </div>
        <div class="card-content">
          <h3>Gateway Status</h3>
          <p>Online</p>
        </div>
      </div>
      <div class="card">
        <div class="card-icon port">
          <i data-lucide="radio" size="20"></i>
        </div>
        <div class="card-content">
          <h3>Proxy Port</h3>
          <p id="portVal">9999</p>
        </div>
      </div>
      <div class="card">
        <div class="card-icon uptime">
          <i data-lucide="clock" size="20"></i>
        </div>
        <div class="card-content">
          <h3>Uptime</h3>
          <p id="uptimeVal">0s</p>
        </div>
      </div>
    </div>

    <div class="section-title">
      <i data-lucide="git-branch" size="16"></i>
      Active Models & Routing Systems
    </div>

    <div class="routes-panel">
      <div class="routes-grid">
        <div class="route-card">
          <div class="route-header">
            <span class="route-source">opencode/big-pickle</span>
            <span class="route-badge opencode">opencode</span>
          </div>
          <div class="route-arrow">│</div>
          <div class="route-dest">
            <i data-lucide="corner-down-right" size="14" class="route-dest-icon"></i>
            <span>big-pickle</span>
          </div>
          <span class="effort-badge">
            <i data-lucide="sparkles" size="11"></i> High Effort
          </span>
        </div>

        <div class="route-card">
          <div class="route-header">
            <span class="route-source">claude_step_3.7_flash</span>
            <span class="route-badge nvidia">nvidia</span>
          </div>
          <div class="route-arrow">│</div>
          <div class="route-dest">
            <i data-lucide="corner-down-right" size="14" class="route-dest-icon"></i>
            <span>step-3.7-flash</span>
          </div>
          <span class="effort-badge">
            <i data-lucide="sparkles" size="11"></i> Max Effort
          </span>
        </div>

        <div class="route-card">
          <div class="route-header">
            <span class="route-source">claude_K_2</span>
            <span class="route-badge nvidia">nvidia</span>
          </div>
          <div class="route-arrow">│</div>
          <div class="route-dest">
            <i data-lucide="corner-down-right" size="14" class="route-dest-icon"></i>
            <span>kimi-k2.6</span>
          </div>
          <span class="effort-badge">
            <i data-lucide="sparkles" size="11"></i> High Effort
          </span>
        </div>

        <div class="route-card">
          <div class="route-header">
            <span class="route-source">claude_M_2.6</span>
            <span class="route-badge opencode">opencode</span>
          </div>
          <div class="route-arrow">│</div>
          <div class="route-dest">
            <i data-lucide="corner-down-right" size="14" class="route-dest-icon"></i>
            <span>mimo-v2.5-free</span>
          </div>
          <span class="effort-badge">
            <i data-lucide="sparkles" size="11"></i> High Effort
          </span>
        </div>
      </div>
    </div>

    <div class="section-title">
      <i data-lucide="terminal" size="16"></i>
      Live Request Streams
    </div>

    <div class="logs-panel">
      <div class="logs-container" id="logsContainer">
        <!-- Javascript renders table contents here -->
      </div>
    </div>
  </div>

  <!-- Overlay Modal for Restarting -->
  <div class="overlay" id="restartOverlay">
    <div class="modal">
      <div class="spinner"></div>
      <h2>Restarting Proxy Gateway</h2>
      <p>Please wait while the service compiles and re-binds...</p>
    </div>
  </div>

  <script>
    lucide.createIcons();

    // Configuration
    const PORT = window.location.port || "9999";
    document.getElementById('portVal').innerText = PORT;

    let lastLogCount = -1;

    // Fetch and render logs
    async function updateDashboard() {
      try {
        const res = await fetch('/dashboard/api/logs');
        if (!res.ok) throw new Error("Fetch failed");
        
        const data = await res.json();
        
        // Update Uptime
        document.getElementById('uptimeVal').innerText = data.uptime;
        
        const logs = data.logs || [];
        
        // Only re-render if the count of logs changed to avoid flashing
        if (logs.length !== lastLogCount) {
          renderLogs(logs);
          lastLogCount = logs.length;
        }
      } catch (err) {
        console.error("Error updating dashboard:", err);
      }
    }

    function renderLogs(logs) {
      const container = document.getElementById('logsContainer');
      
      if (logs.length === 0) {
        container.innerHTML = '<div class="no-logs"><i data-lucide="inbox" size="32"></i><p>No transactions captured yet. Launch queries from your terminal or client!</p></div>';
        lucide.createIcons();
        return;
      }

      // Reverse logs to show newest first
      const reversedLogs = [...logs].reverse();

      let html = '<table>' +
        '<thead>' +
          '<tr>' +
            '<th>Timestamp</th>' +
            '<th>Requested Model</th>' +
            '<th>Translated Route</th>' +
            '<th>Status</th>' +
            '<th>Input Tokens</th>' +
            '<th>Output Tokens</th>' +
          '</tr>' +
        '</thead>' +
        '<tbody>';

      reversedLogs.forEach((log, index) => {
        const isNew = index === 0 && lastLogCount !== -1;
        const rowClass = isNew ? 'class="new-row"' : '';
        
        const date = new Date(log.Timestamp);
        const timeStr = date.toTimeString().split(' ')[0];

        const statusClass = log.Status >= 400 ? 'err' : 'ok';
        const statusText = log.Status >= 400 ? log.Status + ' ERR' : log.Status + ' OK';

        html += '<tr ' + rowClass + '>' +
          '<td class="time-col">' + timeStr + '</td>' +
          '<td class="model-name">' + escapeHTML(log.Model) + '</td>' +
          '<td class="route-target">' + escapeHTML(log.Route) + '</td>' +
          '<td>' +
            '<span class="status-badge ' + statusClass + '">' +
              '<i data-lucide="' + (log.Status >= 400 ? 'alert-triangle' : 'check-circle') + '" size="12"></i>' +
              statusText +
            '</span>' +
          '</td>' +
          '<td><span class="tokens-pill">' + log.TokensIn + '</span></td>' +
          '<td><span class="tokens-pill">' + log.TokensOut + '</span></td>' +
        '</tr>';
      });

      html += '</tbody></table>';

      container.innerHTML = html;
      lucide.createIcons();
    }

    function escapeHTML(str) {
      if (!str) return '';
      return str.replace(/[&<>'"]/g, 
        tag => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' }[tag] || tag)
      );
    }

    // Action: Clear Logs
    document.getElementById('clearBtn').addEventListener('click', async () => {
      try {
        const res = await fetch('/dashboard/api/clear', { method: 'POST' });
        if (res.ok) {
          updateDashboard();
        }
      } catch (err) {
        console.error("Clear logs failed:", err);
      }
    });

    // Action: Restart Proxy
    document.getElementById('restartBtn').addEventListener('click', async () => {
      const overlay = document.getElementById('restartOverlay');
      overlay.classList.add('active');

      try {
        await fetch('/dashboard/api/restart', { method: 'POST' });
      } catch (err) {
        // Expected network disruption due to stop/start
      }

      // Poll health endpoint until it comes back online
      let checkCount = 0;
      const interval = setInterval(async () => {
        checkCount++;
        try {
          const res = await fetch('/health');
          const txt = await res.text();
          if (res.ok && txt.includes("acc-proxy")) {
            clearInterval(interval);
            setTimeout(() => {
              window.location.reload();
            }, 1000);
          }
        } catch (err) {
          // Keep trying
        }

        if (checkCount > 40) { // Timeout after 20 seconds
          clearInterval(interval);
          overlay.querySelector('p').innerText = "Restart is taking longer than expected. Please manually reload the page.";
        }
      }, 500);
    });

    // Start Polling loop
    setInterval(updateDashboard, 1000);
    updateDashboard();
  </script>
</body>
</html>`
