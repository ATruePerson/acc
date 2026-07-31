// Initialize Lucide Icons
lucide.createIcons();

// Configuration & State
const TRUEOX_URL = 'http://localhost:8000';
let isConnected = false;
let conversations = [];
let activeConversationId = null;
let isStreaming = false;

// DOM Elements
const chatInput = document.getElementById('chat-input');
const btnSend = document.getElementById('btn-send');
const messagesViewport = document.getElementById('messages-viewport');
const welcomeScreen = document.getElementById('welcome-screen');
const historyList = document.getElementById('history-list');
const btnNewChat = document.getElementById('btn-new-chat');
const statusDot = document.getElementById('status-dot');
const statusText = document.getElementById('status-text');
const offlineBanner = document.getElementById('offline-banner');
const selectProvider = document.getElementById('select-provider');
const selectModel = document.getElementById('select-model');
const badgeText = document.getElementById('current-badge-text');

// Drawer Elements
const btnToggleTools = document.getElementById('btn-toggle-tools');
const btnCloseTools = document.getElementById('btn-close-tools');
const toolsDrawer = document.getElementById('tools-drawer');

// Settings Modal Elements
const btnSettingsTrigger = document.getElementById('btn-settings-trigger');
const btnSettingsClose = document.getElementById('btn-settings-close');
const settingsOverlay = document.getElementById('settings-overlay');
const settingsOfflineWarning = document.getElementById('settings-offline-warning');
const btnSettingsLoad = document.getElementById('btn-settings-load');
const btnSettingsSave = document.getElementById('btn-settings-save');

// Widgets DOM
const btnRunTts = document.getElementById('btn-run-tts');
const ttsWidgetInput = document.getElementById('tts-widget-input');
const ttsConsole = document.getElementById('tts-widget-console');

const btnRunActiveApp = document.getElementById('btn-run-active-app');
const appConsole = document.getElementById('app-widget-console');

const btnRunApplescript = document.getElementById('btn-run-applescript');
const scriptCode = document.getElementById('applescript-widget-code');
const scriptConsole = document.getElementById('applescript-widget-console');

// ----------------------------------------------------
// 1. Connection Monitor
// ----------------------------------------------------
async function checkTrueoxConnection() {
  try {
    const response = await fetch(TRUEOX_URL + '/api/tools', { method: 'GET' });
    if (response.ok) {
      const data = await response.json();
      if (!isConnected) {
        showToast('Connected to Trueox macOS backend!', 'success');
      }
      isConnected = true;
      statusDot.className = 'status-dot online';
      statusText.innerText = 'Trueox: ONLINE';
      offlineBanner.style.display = 'none';
      settingsOfflineWarning.style.display = 'none';
    } else {
      setOffline();
    }
  } catch (err) {
    setOffline();
  }
}

function setOffline() {
  isConnected = false;
  statusDot.className = 'status-dot offline';
  statusText.innerText = 'Trueox: OFFLINE';
  offlineBanner.style.display = 'flex';
  settingsOfflineWarning.style.display = 'flex';
}

// Start polling connection
setInterval(checkTrueoxConnection, 5000);
checkTrueoxConnection();

// ----------------------------------------------------
// 2. Chat history management (localStorage)
// ----------------------------------------------------
async function fetchAndPopulateProxyInfo() {
  try {
    const response = await fetch('/dashboard/api/info');
    if (response.ok) {
      const info = await response.json();
      
      // 1. Populate providers
      if (info.providers && info.providers.length > 0) {
        const selectProvider = document.getElementById('select-provider');
        const savedValue = selectProvider.value;
        selectProvider.innerHTML = '';
        info.providers.forEach(p => {
          const opt = document.createElement('option');
          opt.value = p;
          // Format nicely (e.g. opencode -> OpenCode, nvidia -> NVIDIA NIM)
          let label = p;
          if (p === 'nvidia') label = 'NVIDIA NIM';
          else if (p === 'opencode') label = 'OpenCode';
          else if (p === 'gemini') label = 'Gemini';
          else if (p === 'openrouter') label = 'OpenRouter';
          else if (p === 'zai') label = 'ZAI';
          else if (p === 'groq') label = 'Groq';
          else if (p === 'ollama') label = 'Ollama';
          else label = p.charAt(0).toUpperCase() + p.slice(1);
          
          opt.innerText = label;
          selectProvider.appendChild(opt);
        });
        // Try to restore saved value or default to gemini or the first item
        if (info.providers.includes(savedValue)) {
          selectProvider.value = savedValue;
        } else if (info.providers.includes('gemini')) {
          selectProvider.value = 'gemini';
        } else {
          selectProvider.value = info.providers[0];
        }
      }
      
      // 2. Populate models from aliases + routes (or from /v1/models directly)
      const modelsRes = await fetch('/v1/models');
      if (modelsRes.ok) {
        const modelsData = await modelsRes.json();
        if (modelsData && modelsData.data) {
          const selectModel = document.getElementById('select-model');
          const savedModelValue = selectModel.value;
          selectModel.innerHTML = '<option value="default" selected>Default Model</option>';
          modelsData.data.forEach(m => {
            const opt = document.createElement('option');
            opt.value = m.id;
            opt.innerText = m.id.replace('anthropic/', '');
            selectModel.appendChild(opt);
          });
          // Try to restore saved model value
          const hasSaved = Array.from(selectModel.options).some(o => o.value === savedModelValue);
          if (hasSaved) {
            selectModel.value = savedModelValue;
          }
        }
      }
      updateBadge();
    }
  } catch (err) {
    console.error('Failed to populate proxy routing systems:', err);
  }
}

function initConversations() {
  fetchAndPopulateProxyInfo();
  const stored = localStorage.getItem('trueox_conversations');
  if (stored) {
    try {
      conversations = JSON.parse(stored);
    } catch (e) {
      conversations = [];
    }
  }
  
  if (conversations.length === 0) {
    startNewConversation();
  } else {
    activeConversationId = conversations[0].id;
    renderHistoryList();
    loadConversation(activeConversationId);
  }
}

function saveConversations() {
  localStorage.setItem('trueox_conversations', JSON.stringify(conversations));
}

function startNewConversation() {
  const newId = 'conv_' + Date.now();
  const newConv = {
    id: newId,
    title: 'New Chat',
    provider: selectProvider.value,
    model: selectModel.value,
    messages: []
  };
  
  conversations.unshift(newConv);
  activeConversationId = newId;
  saveConversations();
  renderHistoryList();
  loadConversation(newId);
}

function renderHistoryList() {
  historyList.innerHTML = '';
  conversations.forEach(conv => {
    const li = document.createElement('li');
    li.className = 'history-item ' + (conv.id === activeConversationId ? 'active' : '');
    li.onclick = () => loadConversation(conv.id);

    const left = document.createElement('div');
    left.className = 'history-item-left';
    
    const bubbleIcon = document.createElement('i');
    bubbleIcon.setAttribute('data-lucide', 'message-square');
    bubbleIcon.style.width = '14px';
    bubbleIcon.style.height = '14px';
    
    const titleSpan = document.createElement('span');
    titleSpan.className = 'history-item-title';
    titleSpan.innerText = conv.title;

    left.appendChild(bubbleIcon);
    left.appendChild(titleSpan);

    const deleteBtn = document.createElement('button');
    deleteBtn.className = 'btn-delete-history';
    deleteBtn.title = 'Delete Chat';
    deleteBtn.onclick = (e) => {
      e.stopPropagation();
      deleteConversation(conv.id);
    };
    
    const trashIcon = document.createElement('i');
    trashIcon.setAttribute('data-lucide', 'trash-2');
    trashIcon.style.width = '13px';
    trashIcon.style.height = '13px';
    deleteBtn.appendChild(trashIcon);

    li.appendChild(left);
    li.appendChild(deleteBtn);
    historyList.appendChild(li);
  });
  lucide.createIcons();
}

function loadConversation(id) {
  activeConversationId = id;
  const conv = conversations.find(c => c.id === id);
  if (!conv) return;

  // Update active selection UI
  document.querySelectorAll('.history-item').forEach(item => item.classList.remove('active'));
  renderHistoryList();

  // Load parameters
  selectProvider.value = conv.provider;
  selectModel.value = conv.model;
  updateBadge();

  // Clear viewport
  messagesViewport.innerHTML = '';
  
  if (conv.messages.length === 0) {
    messagesViewport.appendChild(welcomeScreen);
    welcomeScreen.style.display = 'flex';
  } else {
    welcomeScreen.style.display = 'none';
    conv.messages.forEach(msg => {
      renderMessage(msg.role, msg.content, msg.steps || []);
    });
  }
  scrollToBottom();
}

function deleteConversation(id) {
  conversations = conversations.filter(c => c.id !== id);
  saveConversations();
  
  if (activeConversationId === id) {
    if (conversations.length > 0) {
      activeConversationId = conversations[0].id;
      loadConversation(activeConversationId);
    } else {
      startNewConversation();
    }
  } else {
    renderHistoryList();
  }
  showToast('Conversation deleted', 'success');
}

// ----------------------------------------------------
// 3. UI helpers (scrolling, badges, markdown rendering)
// ----------------------------------------------------
function scrollToBottom() {
  messagesViewport.scrollTop = messagesViewport.scrollHeight;
}

function updateBadge() {
  badgeText.innerText = selectProvider.value + ' / ' + (selectModel.value === 'default' ? 'default model' : selectModel.value);
}

function prefillPrompt(text) {
  chatInput.value = text;
  chatInput.focus();
  btnSend.disabled = false;
  adjustTextareaHeight();
}

function adjustTextareaHeight() {
  chatInput.style.height = '44px';
  chatInput.style.height = (chatInput.scrollHeight - 4) + 'px';
}

// Markdown Render function safely integrating Marked.js if ready
function formatMarkdown(text) {
  if (window.marked && typeof marked.parse === 'function') {
    // Custom parser settings to render paragraphs and headers beautifully
    return marked.parse(text);
  }
  // Simple fallback text sanitizer
  return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
             .replace(/\n/g, "<br>");
}

function renderMessage(role, content, steps = []) {
  welcomeScreen.style.display = 'none';

  const row = document.createElement('div');
  row.className = 'message-row ' + role;

  const bubble = document.createElement('div');
  bubble.className = 'bubble';

  // Attach any system steps first inside assistant messages
  if (role === 'assistant' && steps.length > 0) {
    steps.forEach(stepText => {
      const stepBlock = document.createElement('div');
      stepBlock.className = 'agent-step-block';
      stepBlock.innerHTML = '<div class="agent-step-header"><i data-lucide="settings"></i> System Integration Step</div><div class="agent-step-content">' + stepText + '</div>';
      bubble.appendChild(stepBlock);
    });
  }

  const contentDiv = document.createElement('div');
  contentDiv.className = 'bubble-content-text';
  contentDiv.innerHTML = formatMarkdown(content);
  bubble.appendChild(contentDiv);

  row.appendChild(bubble);
  messagesViewport.appendChild(row);
  scrollToBottom();
  lucide.createIcons();
}

// ----------------------------------------------------
// 4. NDJSON Agent Loop Stream Client
// ----------------------------------------------------
async function submitUserPrompt() {
  const prompt = chatInput.value.trim();
  if (!prompt || isStreaming) return;

  const currentConv = conversations.find(c => c.id === activeConversationId);
  if (!currentConv) return;

  // Disable inputs
  isStreaming = true;
  chatInput.value = '';
  btnSend.disabled = true;
  adjustTextareaHeight();

  // Add user message locally
  currentConv.messages.push({ role: 'user', content: prompt });
  renderMessage('user', prompt);

  // Auto-title conversation if it was named 'New Chat'
  if (currentConv.title === 'New Chat') {
    const words = prompt.split(' ');
    currentConv.title = words.slice(0, 4).join(' ') + (words.length > 4 ? '...' : '');
    renderHistoryList();
  }

  // Add assistant placeholder
  const row = document.createElement('div');
  row.className = 'message-row assistant';
  
  const bubble = document.createElement('div');
  bubble.className = 'bubble';

  // Steps area within bubble
  const stepsContainer = document.createElement('div');
  bubble.appendChild(stepsContainer);

  const contentDiv = document.createElement('div');
  contentDiv.className = 'bubble-content-text';
  contentDiv.innerHTML = '<span style="color: var(--text-darker);">Thinking...</span>';
  bubble.appendChild(contentDiv);

  row.appendChild(bubble);
  messagesViewport.appendChild(row);
  scrollToBottom();

  let assistantContent = '';
  let collectedSteps = [];

  try {
    const payload = {
      messages: currentConv.messages.map(m => ({ role: m.role, content: m.content })),
      provider: selectProvider.value
    };
    if (selectModel.value !== 'default') {
      payload.model = selectModel.value;
    }

    const response = await fetch(TRUEOX_URL + '/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });

    if (!response.ok) {
      throw new Error('HTTP Error ' + response.status);
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder('utf-8');
    let buffer = '';

    while (true) {
      const { value, done } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split('\n');
      buffer = lines.pop(); // Hold onto final incomplete fragment

      for (const line of lines) {
        if (!line.trim()) continue;
        try {
          const packet = JSON.parse(line);
          
          if (packet.step) {
            // Live visual system tool executions
            collectedSteps.push(packet.step);
            
            const stepBlock = document.createElement('div');
            stepBlock.className = 'agent-step-block';
            stepBlock.innerHTML = '<div class="agent-step-header"><i data-lucide="settings"></i> System Integration Step</div><div class="agent-step-content">' + packet.step + '</div>';
            stepsContainer.appendChild(stepBlock);
            lucide.createIcons();
            scrollToBottom();

          } else if (packet.action_start) {
            const toolName = packet.action_start.name;
            const toolArgs = JSON.stringify(packet.action_start.arguments || {});
            const stepText = "Calling " + toolName + " with " + toolArgs;
            collectedSteps.push(stepText);
            
            const stepBlock = document.createElement('div');
            stepBlock.className = 'agent-step-block';
            stepBlock.innerHTML = '<div class="agent-step-header"><i data-lucide="play"></i> System Tool: ' + toolName + '</div><div class="agent-step-content">Arguments: ' + toolArgs + '</div>';
            stepsContainer.appendChild(stepBlock);
            lucide.createIcons();
            scrollToBottom();

          } else if (packet.action_complete) {
            const toolName = packet.action_complete.name;
            const toolResult = typeof packet.action_complete.result === 'object' ? JSON.stringify(packet.action_complete.result) : packet.action_complete.result;
            const stepText = "Completed " + toolName + " with result: " + toolResult;
            collectedSteps.push(stepText);

            const stepBlock = document.createElement('div');
            stepBlock.className = 'agent-step-block';
            stepBlock.style.borderLeftColor = 'var(--green)';
            stepBlock.innerHTML = '<div class="agent-step-header" style="color: var(--green);"><i data-lucide="check-circle-2"></i> System Tool: ' + toolName + ' Done</div><div class="agent-step-content" style="color: var(--text-gray);">' + toolResult + '</div>';
            stepsContainer.appendChild(stepBlock);
            lucide.createIcons();
            scrollToBottom();

          } else if (packet.response || packet.text) {
            const text = packet.response || packet.text;
            if (contentDiv.innerHTML.includes('Thinking...')) {
              contentDiv.innerHTML = '';
            }
            assistantContent += text;
            contentDiv.innerHTML = formatMarkdown(assistantContent);
            scrollToBottom();

          } else if (packet.done) {
            if (contentDiv.innerHTML.includes('Thinking...')) {
              contentDiv.innerHTML = '<span style="color: var(--text-gray); font-style: italic;">Command execution completed.</span>';
            }

          } else if (packet.error) {
            const errorBlock = document.createElement('div');
            errorBlock.className = 'agent-step-block';
            errorBlock.style.borderLeftColor = 'var(--red)';
            errorBlock.innerHTML = '<div class="agent-step-header" style="color:var(--red);"><i data-lucide="alert-circle"></i> Execution Error</div><div class="agent-step-content" style="color:#fca5a5;">' + packet.error + '</div>';
            stepsContainer.appendChild(errorBlock);
            lucide.createIcons();
            scrollToBottom();
          }
        } catch (err) {
          console.warn('NDJSON parsing error', err, line);
        }
      }
    }
  } catch (err) {
    console.error(err);
    contentDiv.innerHTML = '<span style="color: var(--red);">Error connecting to Trueox. Make sure the server on port 8000 is online.</span>';
    showToast('Trueox API connection lost', 'error');
  } finally {
    isStreaming = false;
    btnSend.disabled = false;
    
    // Save complete dialogue to conversation block
    if (assistantContent || collectedSteps.length > 0) {
      currentConv.messages.push({
        role: 'assistant',
        content: assistantContent || "Command execution completed.",
        steps: collectedSteps
      });
      saveConversations();
    }
  }
}

// ----------------------------------------------------
// 5. Settings Modal Sync and Dotenv Save
// ----------------------------------------------------
async function loadSettingsFromTrueox() {
  try {
    const res = await fetch(TRUEOX_URL + '/api/settings', { method: 'GET' });
    if (res.ok) {
      const keys = await res.json();
      document.getElementById('key-gemini').value = keys.gemini_api_key || keys.GEMINI_API_KEY || '';
      document.getElementById('key-groq').value = keys.groq_api_key || keys.GROQ_API_KEY || '';
      document.getElementById('key-openrouter').value = keys.openrouter_api_key || keys.OPENROUTER_API_KEY || '';
      document.getElementById('key-nvidia').value = keys.nvidia_nim_api_key || keys.NVIDIA_NIM_API_KEY || '';
      document.getElementById('key-zai').value = keys.zai_api_key || keys.ZAI_API_KEY || '';
      document.getElementById('key-opencode').value = keys.opencode_api_key || keys.OPENCODE_API_KEY || '';
      showToast('Loaded active environment keys from Trueox .env!', 'success');
    } else {
      showToast('Could not fetch settings from backend.', 'error');
    }
  } catch (e) {
    showToast('Backend offline. Settings unloaded.', 'error');
  }
}

async function saveSettingsToTrueox() {
  const payload = {
    provider: selectProvider.value,
    gemini_api_key: document.getElementById('key-gemini').value,
    groq_api_key: document.getElementById('key-groq').value,
    openrouter_api_key: document.getElementById('key-openrouter').value,
    nvidia_nim_api_key: document.getElementById('key-nvidia').value,
    zai_api_key: document.getElementById('key-zai').value,
    opencode_api_key: document.getElementById('key-opencode').value,
    
    GEMINI_API_KEY: document.getElementById('key-gemini').value,
    GROQ_API_KEY: document.getElementById('key-groq').value,
    OPENROUTER_API_KEY: document.getElementById('key-openrouter').value,
    NVIDIA_NIM_API_KEY: document.getElementById('key-nvidia').value,
    ZAI_API_KEY: document.getElementById('key-zai').value,
    OPENCODE_API_KEY: document.getElementById('key-opencode').value
  };

  try {
    const res = await fetch(TRUEOX_URL + '/api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });

    if (res.ok) {
      showToast('Trueox .env settings updated!', 'success');
      settingsOverlay.style.display = 'none';
    } else {
      showToast('Failed to save settings to server.', 'error');
    }
  } catch (err) {
    showToast('Connection error writing settings.', 'error');
  }
}

// ----------------------------------------------------
// 6. Native Direct Command Execute Console Panel
// ----------------------------------------------------
async function executeDirectTool(toolName, args, consoleEl) {
  consoleEl.innerHTML = '<span style="color: var(--gold);">> Running ' + toolName + '...</span>';
  try {
    const res = await fetch(TRUEOX_URL + '/api/execute', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        tool_name: toolName,
        arguments: args
      })
    });

    if (res.ok) {
      const data = await res.json();
      consoleEl.innerHTML = '<span style="color: var(--green);">> Success</span>\n' + JSON.stringify(data.result || data, null, 2);
      showToast('Direct execution of ' + toolName + ' complete!', 'success');
    } else {
      const text = await res.text();
      consoleEl.innerHTML = '<span style="color: var(--red);">> Failed (HTTP ' + res.status + ')</span>\n' + text;
    }
  } catch (err) {
    consoleEl.innerHTML = '<span style="color: var(--red);">> Error: Connection Refused</span>';
  }
}

// ----------------------------------------------------
// 7. Toast & UI Events Linking
// ----------------------------------------------------
function showToast(message, type = 'default') {
  const container = document.getElementById('toast-container');
  const toast = document.createElement('div');
  toast.className = 'toast ' + type;
  
  const icon = document.createElement('i');
  icon.setAttribute('data-lucide', type === 'success' ? 'check-circle' : type === 'error' ? 'alert-triangle' : 'info');
  icon.style.width = '16px';
  icon.style.height = '16px';

  const text = document.createElement('span');
  text.innerText = message;

  toast.appendChild(icon);
  toast.appendChild(text);
  container.appendChild(toast);
  lucide.createIcons();

  setTimeout(() => {
    toast.style.opacity = '0';
    toast.style.transform = 'translateY(10px)';
    setTimeout(() => toast.remove(), 300);
  }, 3500);
}

// Event listeners
chatInput.addEventListener('input', () => {
  btnSend.disabled = chatInput.value.trim().length === 0;
  adjustTextareaHeight();
});

chatInput.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault();
    submitUserPrompt();
  }
});

btnSend.onclick = submitUserPrompt;
btnNewChat.onclick = startNewConversation;

selectProvider.onchange = () => {
  const conv = conversations.find(c => c.id === activeConversationId);
  if (conv) {
    conv.provider = selectProvider.value;
    saveConversations();
    updateBadge();
  }
};

selectModel.onchange = () => {
  const conv = conversations.find(c => c.id === activeConversationId);
  if (conv) {
    conv.model = selectModel.value;
    saveConversations();
    updateBadge();
  }
};

// Tools Drawer Toggle
btnToggleTools.onclick = () => {
  toolsDrawer.classList.toggle('collapsed');
  btnToggleTools.classList.toggle('active');
};
btnCloseTools.onclick = () => {
  toolsDrawer.classList.add('collapsed');
  btnToggleTools.classList.remove('active');
};

// Settings trigger
btnSettingsTrigger.onclick = () => {
  settingsOverlay.style.display = 'flex';
  loadSettingsFromTrueox();
};
btnSettingsClose.onclick = () => settingsOverlay.style.display = 'none';
btnSettingsLoad.onclick = loadSettingsFromTrueox;
btnSettingsSave.onclick = saveSettingsToTrueox;

// Widget triggers
btnRunTts.onclick = () => {
  const text = ttsWidgetInput.value.trim();
  if (!text) return;
  executeDirectTool('speak_text', { text }, ttsConsole);
};

btnRunActiveApp.onclick = () => {
  executeDirectTool('get_active_app', {}, appConsole);
};

btnRunApplescript.onclick = () => {
  const code = scriptCode.value.trim();
  if (!code) return;
  executeDirectTool('run_custom_script', { script_code: code }, scriptConsole);
};

// Initialize state on load
initConversations();
