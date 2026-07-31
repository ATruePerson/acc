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
