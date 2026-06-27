package main

import (
	"net/http"
)

func (s *server) handleApp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(appHTML))
}

const appHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>ACC Assistant — Trueox macOS Agent</title>
  <link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;500;600;700&family=DM+Mono:wght@400;500&display=swap" rel="stylesheet">
  <script src="https://cdn.jsdelivr.net/npm/marked/marked.min.js"></script>
  <script src="https://cdn.jsdelivr.net/npm/lucide@0.344.0/dist/umd/lucide.min.js"></script>
  <style>
    :root {
      --bg-dark: #0b0b0d;
      --bg-sidebar: #131316;
      --bg-panel: #18181c;
      --bg-card: rgba(30, 30, 36, 0.45);
      --border-color: rgba(255, 255, 255, 0.05);
      --border-hover: rgba(217, 119, 6, 0.25);
      
      --text-main: #f4f4f5;
      --text-gray: #a1a1aa;
      --text-darker: #71717a;
      
      --gold: #d97706;
      --gold-bright: #fbbf24;
      --gold-glow: rgba(217, 119, 6, 0.15);
      --gold-glow-strong: rgba(217, 119, 6, 0.4);
      
      --green: #10b981;
      --green-glow: rgba(16, 185, 129, 0.3);
      --red: #ef4444;
      --red-glow: rgba(239, 68, 68, 0.3);
      --purple: #8b5cf6;
      --purple-glow: rgba(139, 92, 246, 0.3);
      
      --transition-main: all 0.3s cubic-bezier(0.16, 1, 0.3, 1);
    }

    * {
      box-sizing: border-box;
      margin: 0;
      padding: 0;
    }

    body {
      font-family: 'Outfit', -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      background-color: var(--bg-dark);
      color: var(--text-main);
      height: 100vh;
      overflow: hidden;
      display: flex;
    }

    /* Container layout */
    .app-container {
      display: flex;
      width: 100%;
      height: 100%;
      position: relative;
    }

    /* Sidebar styles */
    .sidebar {
      width: 280px;
      background-color: var(--bg-sidebar);
      border-right: 1px solid var(--border-color);
      display: flex;
      flex-direction: column;
      height: 100%;
      flex-shrink: 0;
      transition: var(--transition-main);
      z-index: 10;
    }

    .sidebar-header {
      padding: 1.5rem;
      border-bottom: 1px solid var(--border-color);
      display: flex;
      justify-content: space-between;
      align-items: center;
    }

    .brand {
      display: flex;
      align-items: center;
      gap: 0.75rem;
    }

    .brand-logo {
      width: 32px;
      height: 32px;
      background: radial-gradient(circle, var(--gold-bright) 0%, var(--gold) 100%);
      border-radius: 8px;
      display: flex;
      align-items: center;
      justify-content: center;
      box-shadow: 0 0 15px var(--gold-glow-strong);
      animation: pulseLogo 3s infinite ease-in-out;
    }

    .brand-logo i {
      color: var(--bg-dark);
      width: 16px;
      height: 16px;
    }

    .brand-title {
      font-size: 1.1rem;
      font-weight: 600;
      letter-spacing: -0.01em;
      background: linear-gradient(135deg, #fff 0%, #a1a1aa 100%);
      -webkit-background-clip: text;
      -webkit-text-fill-color: transparent;
    }

    .btn-new-chat {
      margin: 1.25rem;
      padding: 0.75rem 1rem;
      border-radius: 8px;
      border: 1px solid rgba(217, 119, 6, 0.3);
      background-color: var(--gold-glow);
      color: var(--gold-bright);
      font-weight: 500;
      display: flex;
      align-items: center;
      justify-content: center;
      gap: 0.5rem;
      cursor: pointer;
      font-family: inherit;
      transition: var(--transition-main);
    }

    .btn-new-chat:hover {
      background-color: rgba(217, 119, 6, 0.25);
      border-color: var(--gold);
      box-shadow: 0 0 12px var(--gold-glow);
      transform: translateY(-1px);
    }

    .history-container {
      flex: 1;
      overflow-y: auto;
      padding: 0 0.75rem;
    }

    .history-title {
      font-size: 0.75rem;
      text-transform: uppercase;
      letter-spacing: 0.05em;
      color: var(--text-darker);
      margin: 1rem 0.5rem 0.5rem 0.5rem;
      font-weight: 600;
    }

    .history-list {
      list-style: none;
    }

    .history-item {
      padding: 0.75rem;
      border-radius: 8px;
      margin-bottom: 0.25rem;
      cursor: pointer;
      display: flex;
      align-items: center;
      justify-content: space-between;
      transition: var(--transition-main);
      color: var(--text-gray);
      font-size: 0.9rem;
    }

    .history-item:hover, .history-item.active {
      background-color: var(--bg-panel);
      color: var(--text-main);
    }

    .history-item.active {
      border-left: 2px solid var(--gold);
      border-radius: 0 8px 8px 0;
    }

    .history-item-left {
      display: flex;
      align-items: center;
      gap: 0.65rem;
      overflow: hidden;
      white-space: nowrap;
      text-overflow: ellipsis;
      flex: 1;
    }

    .history-item-title {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .btn-delete-history {
      opacity: 0;
      background: none;
      border: none;
      color: var(--text-darker);
      cursor: pointer;
      padding: 2px;
      border-radius: 4px;
      transition: var(--transition-main);
    }

    .history-item:hover .btn-delete-history {
      opacity: 1;
    }

    .btn-delete-history:hover {
      color: var(--red);
      background-color: rgba(239, 68, 68, 0.1);
    }

    .sidebar-footer {
      padding: 1.25rem;
      border-top: 1px solid var(--border-color);
      display: flex;
      align-items: center;
      justify-content: space-between;
      background-color: rgba(0,0,0,0.15);
    }

    .connection-status {
      display: flex;
      align-items: center;
      gap: 0.65rem;
      font-size: 0.85rem;
    }

    .status-dot {
      width: 8px;
      height: 8px;
      border-radius: 50%;
      position: relative;
    }

    .status-dot.online {
      background-color: var(--green);
      box-shadow: 0 0 10px var(--green-glow);
    }

    .status-dot.online::after {
      content: '';
      position: absolute;
      width: 100%;
      height: 100%;
      border-radius: 50%;
      background-color: var(--green);
      animation: pulseDot 2s infinite ease-out;
      opacity: 0.6;
    }

    .status-dot.offline {
      background-color: var(--red);
      box-shadow: 0 0 10px var(--red-glow);
    }

    .btn-settings {
      background: none;
      border: none;
      color: var(--text-gray);
      cursor: pointer;
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 0.5rem;
      border-radius: 8px;
      transition: var(--transition-main);
    }

    .btn-settings:hover {
      color: var(--gold-bright);
      background-color: var(--bg-panel);
    }

    /* Main Chat Panel */
    .main-chat {
      flex: 1;
      display: flex;
      flex-direction: column;
      height: 100%;
      background-color: var(--bg-dark);
      position: relative;
    }

    /* Top Navigation bar */
    .chat-header {
      height: 64px;
      border-bottom: 1px solid var(--border-color);
      display: flex;
      justify-content: space-between;
      align-items: center;
      padding: 0 1.5rem;
      background-color: var(--bg-sidebar);
      z-index: 5;
    }

    .chat-header-info {
      display: flex;
      align-items: center;
      gap: 0.75rem;
    }

    .provider-model-badge {
      padding: 0.35rem 0.75rem;
      background-color: var(--bg-panel);
      border: 1px solid var(--border-color);
      border-radius: 20px;
      font-size: 0.8rem;
      color: var(--text-gray);
      display: flex;
      align-items: center;
      gap: 0.5rem;
    }

    .provider-model-badge i {
      color: var(--gold-bright);
      width: 12px;
      height: 12px;
    }

    .header-actions {
      display: flex;
      align-items: center;
      gap: 0.75rem;
    }

    .btn-action-icon {
      background: none;
      border: 1px solid var(--border-color);
      color: var(--text-gray);
      width: 36px;
      height: 36px;
      border-radius: 8px;
      cursor: pointer;
      display: flex;
      align-items: center;
      justify-content: center;
      transition: var(--transition-main);
    }

    .btn-action-icon:hover {
      color: var(--gold-bright);
      background-color: var(--bg-panel);
      border-color: var(--gold);
    }

    .btn-action-icon.active {
      color: var(--gold-bright);
      background-color: var(--gold-glow);
      border-color: var(--gold);
    }

    /* Chat Messages scroll area */
    .messages-viewport {
      flex: 1;
      overflow-y: auto;
      padding: 2rem 10% 4rem 10%;
      display: flex;
      flex-direction: column;
      gap: 1.5rem;
    }

    /* Welcome screen when empty */
    .welcome-screen {
      display: flex;
      flex-direction: column;
      align-items: center;
      justify-content: center;
      height: 100%;
      text-align: center;
      padding: 2rem;
      animation: fadeIn 0.8s ease-out;
    }

    .welcome-logo {
      width: 72px;
      height: 72px;
      background: radial-gradient(circle, var(--gold-bright) 0%, var(--gold) 100%);
      border-radius: 18px;
      display: flex;
      align-items: center;
      justify-content: center;
      box-shadow: 0 0 35px var(--gold-glow-strong);
      margin-bottom: 1.5rem;
    }

    .welcome-logo i {
      width: 36px;
      height: 36px;
      color: var(--bg-dark);
    }

    .welcome-screen h2 {
      font-size: 1.8rem;
      font-weight: 700;
      margin-bottom: 0.5rem;
      letter-spacing: -0.02em;
    }

    .welcome-screen p {
      color: var(--text-gray);
      font-size: 1rem;
      max-width: 480px;
      margin-bottom: 2rem;
      line-height: 1.5;
    }

    .suggestions-grid {
      display: grid;
      grid-template-columns: repeat(2, minmax(220px, 1fr));
      gap: 1rem;
      width: 100%;
      max-width: 580px;
    }

    .suggestion-card {
      background: var(--bg-card);
      border: 1px solid var(--border-color);
      border-radius: 12px;
      padding: 1.25rem;
      text-align: left;
      cursor: pointer;
      transition: var(--transition-main);
      display: flex;
      flex-direction: column;
      gap: 0.5rem;
    }

    .suggestion-card:hover {
      border-color: var(--gold);
      background-color: rgba(30, 30, 36, 0.8);
      transform: translateY(-2px);
      box-shadow: 0 8px 24px rgba(0,0,0,0.3);
    }

    .suggestion-icon {
      color: var(--gold-bright);
      width: 20px;
      height: 20px;
      margin-bottom: 0.25rem;
    }

    .suggestion-title {
      font-size: 0.95rem;
      font-weight: 600;
      color: var(--text-main);
    }

    .suggestion-desc {
      font-size: 0.8rem;
      color: var(--text-gray);
      line-height: 1.4;
    }

    /* Message Bubbles */
    .message-row {
      display: flex;
      width: 100%;
      margin-bottom: 0.5rem;
      animation: messageSlideIn 0.4s cubic-bezier(0.16, 1, 0.3, 1);
    }

    .message-row.user {
      justify-content: flex-end;
    }

    .message-row.assistant {
      justify-content: flex-start;
    }

    .bubble {
      max-width: 80%;
      border-radius: 16px;
      padding: 1.25rem;
      position: relative;
      line-height: 1.6;
      font-size: 0.95rem;
    }

    .message-row.user .bubble {
      background-color: var(--bg-card);
      border: 1px solid var(--border-color);
      color: var(--text-main);
      border-bottom-right-radius: 4px;
    }

    .message-row.user .bubble:hover {
      border-color: var(--border-hover);
    }

    .message-row.assistant .bubble {
      background-color: rgba(0,0,0,0);
      color: var(--text-main);
      border-bottom-left-radius: 4px;
      padding-left: 0;
      padding-right: 0;
    }

    /* Markdown styling inside assistant bubble */
    .bubble p {
      margin-bottom: 0.75rem;
    }

    .bubble p:last-child {
      margin-bottom: 0;
    }

    .bubble ul, .bubble ol {
      margin-bottom: 0.75rem;
      padding-left: 1.5rem;
    }

    .bubble li {
      margin-bottom: 0.25rem;
    }

    .bubble code {
      font-family: 'DM Mono', monospace;
      font-size: 0.85rem;
      background-color: rgba(255,255,255,0.06);
      padding: 0.15rem 0.3rem;
      border-radius: 4px;
      color: var(--gold-bright);
    }

    .bubble pre {
      background-color: #101012;
      border: 1px solid var(--border-color);
      border-radius: 8px;
      padding: 1rem;
      margin: 1rem 0;
      overflow-x: auto;
      position: relative;
    }

    .bubble pre code {
      background: none;
      padding: 0;
      color: var(--text-main);
      font-size: 0.85rem;
    }

    .code-block-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      background-color: #18181c;
      padding: 0.5rem 1rem;
      border-top-left-radius: 8px;
      border-top-right-radius: 8px;
      border: 1px solid var(--border-color);
      border-bottom: none;
      font-family: 'DM Mono', monospace;
      font-size: 0.75rem;
      color: var(--text-gray);
      margin-top: 1rem;
    }

    .code-block-header + pre {
      margin-top: 0;
      border-top-left-radius: 0;
      border-top-right-radius: 0;
    }

    .btn-copy-code {
      background: none;
      border: none;
      color: var(--text-darker);
      cursor: pointer;
      display: flex;
      align-items: center;
      gap: 0.25rem;
      font-size: 0.7rem;
      transition: var(--transition-main);
    }

    .btn-copy-code:hover {
      color: var(--gold-bright);
    }

    /* Agent step blocks (gorgeous console output) */
    .agent-step-block {
      background-color: #0b0b0d;
      border-left: 3px solid var(--gold);
      border-radius: 0 6px 6px 0;
      margin: 0.75rem 0;
      padding: 0.75rem 1rem;
      font-family: 'DM Mono', monospace;
      font-size: 0.8rem;
      color: #92929c;
      display: flex;
      flex-direction: column;
      gap: 0.35rem;
      animation: fadeIn 0.3s ease;
    }

    .agent-step-header {
      display: flex;
      align-items: center;
      gap: 0.5rem;
      font-weight: 500;
      color: var(--gold-bright);
      text-transform: uppercase;
      font-size: 0.75rem;
      letter-spacing: 0.05em;
    }

    .agent-step-header i {
      width: 12px;
      height: 12px;
      animation: spinGear 4s infinite linear;
    }

    .agent-step-content {
      white-space: pre-wrap;
      word-break: break-all;
      line-height: 1.4;
    }

    /* Input Dock Area */
    .input-dock {
      padding: 0 10% 2rem 10%;
      background: linear-gradient(180deg, rgba(11,11,13,0) 0%, var(--bg-dark) 40%);
      z-index: 5;
    }

    .input-box-wrapper {
      background-color: var(--bg-panel);
      border: 1px solid var(--border-color);
      border-radius: 16px;
      padding: 0.75rem 1.25rem;
      display: flex;
      flex-direction: column;
      gap: 0.75rem;
      transition: var(--transition-main);
      box-shadow: 0 10px 30px rgba(0,0,0,0.4);
    }

    .input-box-wrapper:focus-within {
      border-color: var(--gold);
      box-shadow: 0 0 18px var(--gold-glow);
    }

    .input-textarea {
      background: none;
      border: none;
      color: var(--text-main);
      font-family: inherit;
      font-size: 0.95rem;
      resize: none;
      outline: none;
      width: 100%;
      height: 44px;
      min-height: 44px;
      max-height: 200px;
      line-height: 1.5;
    }

    .input-controls {
      display: flex;
      justify-content: space-between;
      align-items: center;
      border-top: 1px solid rgba(255,255,255,0.03);
      padding-top: 0.65rem;
    }

    .input-controls-left {
      display: flex;
      align-items: center;
      gap: 0.5rem;
    }

    /* Direct selector in controls */
    .selector-container {
      position: relative;
      display: flex;
      align-items: center;
      background-color: rgba(255,255,255,0.03);
      border: 1px solid var(--border-color);
      border-radius: 8px;
      padding: 0.25rem 0.5rem;
      gap: 0.25rem;
    }

    .selector-container label {
      font-size: 0.7rem;
      color: var(--text-darker);
      text-transform: uppercase;
      font-weight: 600;
    }

    .select-dropdown {
      background: none;
      border: none;
      color: var(--text-gray);
      font-family: inherit;
      font-size: 0.8rem;
      font-weight: 500;
      outline: none;
      cursor: pointer;
      padding-right: 0.5rem;
    }

    .select-dropdown option {
      background-color: var(--bg-panel);
      color: var(--text-main);
    }

    .btn-send {
      background-color: var(--gold);
      color: var(--bg-dark);
      border: none;
      width: 36px;
      height: 36px;
      border-radius: 50%;
      cursor: pointer;
      display: flex;
      align-items: center;
      justify-content: center;
      transition: var(--transition-main);
    }

    .btn-send:hover {
      background-color: var(--gold-bright);
      transform: scale(1.05);
      box-shadow: 0 0 12px var(--gold-glow-strong);
    }

    .btn-send:disabled {
      background-color: var(--bg-sidebar);
      color: var(--text-darker);
      cursor: not-allowed;
      transform: none;
      box-shadow: none;
    }

    .offline-banner {
      background-color: rgba(239, 68, 68, 0.15);
      border: 1px solid rgba(239, 68, 68, 0.3);
      border-radius: 8px;
      padding: 0.75rem 1rem;
      margin-bottom: 1rem;
      font-size: 0.85rem;
      color: #fca5a5;
      display: none;
      align-items: center;
      gap: 0.75rem;
      animation: fadeIn 0.4s ease;
    }

    /* Right Tools Panel Drawer */
    .tools-drawer {
      width: 340px;
      background-color: var(--bg-sidebar);
      border-left: 1px solid var(--border-color);
      display: flex;
      flex-direction: column;
      height: 100%;
      flex-shrink: 0;
      transition: var(--transition-main);
      z-index: 10;
    }

    .tools-drawer.collapsed {
      width: 0;
      overflow: hidden;
      border-left: none;
    }

    .drawer-header {
      padding: 1.5rem;
      border-bottom: 1px solid var(--border-color);
      display: flex;
      justify-content: space-between;
      align-items: center;
    }

    .drawer-header h3 {
      font-size: 1rem;
      font-weight: 600;
      color: var(--text-main);
      display: flex;
      align-items: center;
      gap: 0.5rem;
    }

    .drawer-header h3 i {
      color: var(--gold-bright);
    }

    .btn-action-icon {
      background: none;
      border: 1px solid var(--border-color);
      color: var(--text-gray);
      width: 36px;
      height: 36px;
      border-radius: 8px;
      cursor: pointer;
      display: flex;
      align-items: center;
      justify-content: center;
      transition: var(--transition-main);
    }

    .btn-action-icon:hover {
      color: var(--gold-bright);
      background-color: var(--bg-panel);
      border-color: var(--gold);
    }

    .drawer-content {
      flex: 1;
      overflow-y: auto;
      padding: 1.5rem;
      display: flex;
      flex-direction: column;
      gap: 1.5rem;
    }

    .tool-widget-card {
      background-color: var(--bg-panel);
      border: 1px solid var(--border-color);
      border-radius: 12px;
      padding: 1.25rem;
      display: flex;
      flex-direction: column;
      gap: 0.75rem;
    }

    .tool-widget-card h4 {
      font-size: 0.85rem;
      font-weight: 600;
      color: var(--text-main);
      text-transform: uppercase;
      letter-spacing: 0.03em;
      display: flex;
      align-items: center;
      gap: 0.5rem;
    }

    .tool-widget-card h4 i {
      color: var(--gold-bright);
      width: 14px;
      height: 14px;
    }

    .tool-input {
      background-color: var(--bg-dark);
      border: 1px solid var(--border-color);
      border-radius: 8px;
      padding: 0.6rem 0.75rem;
      color: var(--text-main);
      font-family: inherit;
      font-size: 0.85rem;
      outline: none;
      transition: var(--transition-main);
    }

    .tool-input:focus {
      border-color: var(--gold);
    }

    .tool-textarea {
      background-color: var(--bg-dark);
      border: 1px solid var(--border-color);
      border-radius: 8px;
      padding: 0.6rem 0.75rem;
      color: var(--text-main);
      font-family: 'DM Mono', monospace;
      font-size: 0.8rem;
      outline: none;
      resize: vertical;
      min-height: 80px;
      transition: var(--transition-main);
    }

    .tool-textarea:focus {
      border-color: var(--gold);
    }

    .btn-execute-widget {
      background-color: var(--bg-sidebar);
      border: 1px solid rgba(217, 119, 6, 0.4);
      color: var(--gold-bright);
      padding: 0.5rem 0.75rem;
      border-radius: 8px;
      font-family: inherit;
      font-size: 0.8rem;
      font-weight: 500;
      cursor: pointer;
      display: flex;
      align-items: center;
      justify-content: center;
      gap: 0.4rem;
      transition: var(--transition-main);
    }

    .btn-execute-widget:hover {
      background-color: var(--gold-glow);
      border-color: var(--gold);
    }

    .widget-console {
      background-color: #0b0b0d;
      border: 1px solid var(--border-color);
      border-radius: 6px;
      padding: 0.5rem 0.75rem;
      font-family: 'DM Mono', monospace;
      font-size: 0.75rem;
      color: #92929c;
      min-height: 36px;
      max-height: 120px;
      overflow-y: auto;
      white-space: pre-wrap;
      word-break: break-all;
    }

    /* Modal dialog (Settings sync) */
    .settings-overlay {
      position: fixed;
      top: 0;
      left: 0;
      right: 0;
      bottom: 0;
      background-color: rgba(11,11,13,0.85);
      backdrop-filter: blur(12px);
      -webkit-backdrop-filter: blur(12px);
      z-index: 100;
      display: none;
      align-items: center;
      justify-content: center;
      animation: fadeIn 0.3s ease;
    }

    .settings-modal {
      width: 100%;
      max-width: 500px;
      background-color: var(--bg-panel);
      border: 1px solid var(--border-color);
      border-radius: 16px;
      box-shadow: 0 20px 50px rgba(0,0,0,0.6);
      display: flex;
      flex-direction: column;
      overflow: hidden;
      animation: modalSlideUp 0.4s cubic-bezier(0.16, 1, 0.3, 1);
    }

    .settings-header {
      padding: 1.5rem;
      border-bottom: 1px solid var(--border-color);
      display: flex;
      justify-content: space-between;
      align-items: center;
    }

    .settings-header h3 {
      font-size: 1.1rem;
      font-weight: 600;
      display: flex;
      align-items: center;
      gap: 0.5rem;
    }

    .settings-header h3 i {
      color: var(--gold-bright);
    }

    .btn-close-modal {
      background: none;
      border: none;
      color: var(--text-gray);
      cursor: pointer;
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 0.25rem;
      border-radius: 50%;
      transition: var(--transition-main);
    }

    .btn-close-modal:hover {
      color: var(--text-main);
      background-color: rgba(255,255,255,0.05);
    }

    .settings-body {
      padding: 1.5rem;
      overflow-y: auto;
      max-height: 400px;
      display: flex;
      flex-direction: column;
      gap: 1.25rem;
    }

    .setting-group {
      display: flex;
      flex-direction: column;
      gap: 0.4rem;
    }

    .setting-group label {
      font-size: 0.8rem;
      font-weight: 600;
      color: var(--text-gray);
      text-transform: uppercase;
      letter-spacing: 0.05em;
    }

    .settings-footer {
      padding: 1.25rem 1.5rem;
      border-top: 1px solid var(--border-color);
      display: flex;
      justify-content: flex-end;
      gap: 0.75rem;
      background-color: rgba(0,0,0,0.15);
    }

    .btn-modal {
      padding: 0.6rem 1.25rem;
      border-radius: 8px;
      font-family: inherit;
      font-size: 0.85rem;
      font-weight: 500;
      cursor: pointer;
      transition: var(--transition-main);
    }

    .btn-modal-cancel {
      background-color: transparent;
      border: 1px solid var(--border-color);
      color: var(--text-gray);
    }

    .btn-modal-cancel:hover {
      color: var(--text-main);
      background-color: rgba(255,255,255,0.03);
    }

    .btn-modal-save {
      background-color: var(--gold);
      border: none;
      color: var(--bg-dark);
    }

    .btn-modal-save:hover {
      background-color: var(--gold-bright);
      box-shadow: 0 0 10px var(--gold-glow-strong);
    }

    /* Toast Notification system */
    .toast-container {
      position: fixed;
      bottom: 2rem;
      right: 2rem;
      display: flex;
      flex-direction: column;
      gap: 0.5rem;
      z-index: 200;
    }

    .toast {
      background-color: var(--bg-panel);
      border-left: 4px solid var(--gold);
      border-radius: 0 8px 8px 0;
      padding: 1rem 1.25rem;
      color: var(--text-main);
      font-size: 0.875rem;
      box-shadow: 0 10px 30px rgba(0,0,0,0.5);
      display: flex;
      align-items: center;
      gap: 0.75rem;
      animation: toastSlideIn 0.3s cubic-bezier(0.16, 1, 0.3, 1);
      border: 1px solid var(--border-color);
      border-left-width: 4px;
    }

    .toast.success {
      border-left-color: var(--green);
    }

    .toast.success i {
      color: var(--green);
    }

    .toast.error {
      border-left-color: var(--red);
    }

    .toast.error i {
      color: var(--red);
    }

    /* Keyframes & Animations */
    @keyframes pulseLogo {
      0%, 100% { transform: scale(1); filter: drop-shadow(0 0 15px var(--gold-glow)); }
      50% { transform: scale(1.05); filter: drop-shadow(0 0 25px var(--gold-glow-strong)); }
    }

    @keyframes pulseDot {
      0% { transform: scale(1); opacity: 0.6; }
      100% { transform: scale(2.4); opacity: 0; }
    }

    @keyframes fadeIn {
      from { opacity: 0; }
      to { opacity: 1; }
    }

    @keyframes messageSlideIn {
      from { opacity: 0; transform: translateY(8px); }
      to { opacity: 1; transform: translateY(0); }
    }

    @keyframes modalSlideUp {
      from { opacity: 0; transform: translateY(16px); }
      to { opacity: 1; transform: translateY(0); }
    }

    @keyframes toastSlideIn {
      from { opacity: 0; transform: translateX(20px); }
      to { opacity: 1; transform: translateX(0); }
    }

    @keyframes spinGear {
      0% { transform: rotate(0deg); }
      100% { transform: rotate(360deg); }
    }
  </style>
</head>
<body>

  <div class="app-container">
    
    <!-- Collapsible Sidebar -->
    <aside class="sidebar" id="sidebar">
      <div class="sidebar-header">
        <div class="brand">
          <div class="brand-logo">
            <i data-lucide="cpu"></i>
          </div>
          <span class="brand-title">ACC Client</span>
        </div>
      </div>

      <button class="btn-new-chat" id="btn-new-chat">
        <i data-lucide="plus"></i> New Chat
      </button>

      <div class="history-container">
        <h3 class="history-title">Recent Conversations</h3>
        <ul class="history-list" id="history-list">
          <!-- Populated by JavaScript -->
        </ul>
      </div>

      <div class="sidebar-footer">
        <div class="connection-status">
          <span class="status-dot offline" id="status-dot"></span>
          <span id="status-text">Connecting...</span>
        </div>
        <button class="btn-settings" id="btn-settings-trigger" title="Trueox Settings">
          <i data-lucide="sliders"></i>
        </button>
      </div>
    </aside>

    <!-- Main Chat Window -->
    <main class="main-chat">
      <header class="chat-header">
        <div class="chat-header-info">
          <div class="provider-model-badge">
            <i data-lucide="sparkles"></i>
            <span id="current-badge-text">gemini / gemini-2.5-flash-lite</span>
          </div>
        </div>

        <div class="header-actions">
          <button class="btn-action-icon" id="btn-toggle-tools" title="Toggle native macOS tools panel">
            <i data-lucide="terminal"></i>
          </button>
        </div>
      </header>

      <!-- Messages Viewport -->
      <div class="messages-viewport" id="messages-viewport">
        <!-- Empty welcome screen -->
        <div class="welcome-screen" id="welcome-screen">
          <div class="welcome-logo">
            <i data-lucide="terminal"></i>
          </div>
          <h2>I'm Trueox macOS Assistant</h2>
          <p>Your agent controller. Type a prompt to run agent loops, execute local AppleScripts, speak aloud, and control your Mac.</p>
          
          <div class="suggestions-grid">
            <div class="suggestion-card" onclick="prefillPrompt('Tell me what app is active right now and say hello!')">
              <i data-lucide="eye" class="suggestion-icon"></i>
              <span class="suggestion-title">Active App & Hello</span>
              <span class="suggestion-desc">Inspect which macOS app is open and speak hello aloud.</span>
            </div>
            <div class="suggestion-card" onclick="prefillPrompt('Create a reminder with title \'Drink water!\' in 5 seconds')">
              <i data-lucide="bell" class="suggestion-icon"></i>
              <span class="suggestion-title">Schedule macOS Reminder</span>
              <span class="suggestion-desc">Add tasks directly into your native Apple Reminders app.</span>
            </div>
            <div class="suggestion-card" onclick="prefillPrompt('Run a custom script to open Safari and speak \'Done\' out loud')">
              <i data-lucide="code" class="suggestion-icon"></i>
              <span class="suggestion-title">Custom AppleScript Run</span>
              <span class="suggestion-desc">Execute high-context custom system commands.</span>
            </div>
            <div class="suggestion-card" onclick="prefillPrompt('Say out loud: \'ACC gateway successfully loaded!\'')">
              <i data-lucide="volume-2" class="suggestion-icon"></i>
              <span class="suggestion-title">Speak Native Voice</span>
              <span class="suggestion-desc">Stream and vocalize messages using native Mac TTS.</span>
            </div>
          </div>
        </div>
      </div>

      <!-- Input Dock -->
      <div class="input-dock">
        <!-- Offline warning banner -->
        <div class="offline-banner" id="offline-banner">
          <i data-lucide="alert-triangle"></i>
          <span>Trueox backend offline on <strong>localhost:8000</strong>. Run <code>python app.py</code> in the Trueox folder.</span>
        </div>

        <div class="input-box-wrapper">
          <textarea class="input-textarea" id="chat-input" placeholder="How can Trueox help you on this Mac today?"></textarea>
          
          <div class="input-controls">
            <div class="input-controls-left">
              <!-- Provider Selection -->
              <div class="selector-container">
                <label>Provider</label>
                <select class="select-dropdown" id="select-provider">
                  <option value="gemini" selected>Gemini</option>
                  <option value="groq">Groq</option>
                  <option value="openrouter">OpenRouter</option>
                  <option value="nvidia">NVIDIA NIM</option>
                  <option value="zai">ZAI</option>
                  <option value="opencode">OpenCode</option>
                  <option value="ollama">Ollama</option>
                </select>
              </div>

              <!-- Model Selection -->
              <div class="selector-container">
                <label>Model Override</label>
                <select class="select-dropdown" id="select-model">
                  <option value="default" selected>Default Model</option>
                  <option value="gemini-2.5-flash-lite">gemini-2.5-flash-lite</option>
                  <option value="gemini-2.5-pro">gemini-2.5-pro</option>
                  <option value="llama-3.3-70b-versatile">llama-3.3-70b</option>
                  <option value="deepseek-r1">deepseek-r1</option>
                  <option value="claude-3-5-sonnet">claude-3-5-sonnet</option>
                </select>
              </div>
            </div>

            <button class="btn-send" id="btn-send" disabled>
              <i data-lucide="arrow-up" style="width: 18px; height: 18px;"></i>
            </button>
          </div>
        </div>
      </div>
    </main>

    <!-- Sliding Native Tools Right Sidebar -->
    <aside class="tools-drawer collapsed" id="tools-drawer">
      <div class="drawer-header">
        <h3><i data-lucide="terminal"></i> Native Mac Controls</h3>
        <button class="btn-action-icon" id="btn-close-tools" style="border:none;">
          <i data-lucide="x"></i>
        </button>
      </div>

      <div class="drawer-content">
        <!-- Speak Text Widget -->
        <div class="tool-widget-card">
          <h4><i data-lucide="volume-2"></i> Speak Text out loud</h4>
          <input type="text" class="tool-input" id="tts-widget-input" placeholder="Text to say..." value="ACC connected successfully.">
          <button class="btn-execute-widget" id="btn-run-tts">
            <i data-lucide="play" style="width:12px;height:12px;"></i> Speak Aloud
          </button>
          <div class="widget-console" id="tts-widget-console">> Idle</div>
        </div>

        <!-- Get Active App Widget -->
        <div class="tool-widget-card">
          <h4><i data-lucide="app-window"></i> Get Active macOS App</h4>
          <button class="btn-execute-widget" id="btn-run-active-app">
            <i data-lucide="refresh-cw" style="width:12px;height:12px;"></i> Inspect Active App
          </button>
          <div class="widget-console" id="app-widget-console">> Idle</div>
        </div>

        <!-- Custom AppleScript executor widget -->
        <div class="tool-widget-card">
          <h4><i data-lucide="file-code"></i> Run Custom AppleScript</h4>
          <textarea class="tool-textarea" id="applescript-widget-code" placeholder="-- Enter AppleScript code here&#10;tell application &quot;Finder&quot; to activate"></textarea>
          <button class="btn-execute-widget" id="btn-run-applescript">
            <i data-lucide="terminal" style="width:12px;height:12px;"></i> Execute AppleScript
          </button>
          <div class="widget-console" id="applescript-widget-console" style="min-height: 80px;">> Idle</div>
        </div>
      </div>
    </aside>

  </div>

  <!-- Settings Overlay Modal -->
  <div class="settings-overlay" id="settings-overlay">
    <div class="settings-modal">
      <div class="settings-header">
        <h3><i data-lucide="sliders"></i> Trueox Settings Sync</h3>
        <button class="btn-close-modal" id="btn-settings-close">
          <i data-lucide="x"></i>
        </button>
      </div>
      
      <div class="settings-body">
        <div class="offline-banner" id="settings-offline-warning" style="display:flex; margin-bottom:0;">
          <i data-lucide="alert-circle"></i>
          <span>Keys are saved directly to Trueox .env. Server must be online.</span>
        </div>

        <div class="setting-group">
          <label>GEMINI_API_KEY</label>
          <input type="password" class="tool-input" id="key-gemini" placeholder="AIzaSy...">
        </div>
        <div class="setting-group">
          <label>GROQ_API_KEY</label>
          <input type="password" class="tool-input" id="key-groq" placeholder="gsk_...">
        </div>
        <div class="setting-group">
          <label>OPENROUTER_API_KEY</label>
          <input type="password" class="tool-input" id="key-openrouter" placeholder="sk-or-v1-...">
        </div>
        <div class="setting-group">
          <label>NVIDIA_NIM_API_KEY</label>
          <input type="password" class="tool-input" id="key-nvidia" placeholder="nvapi-...">
        </div>
        <div class="setting-group">
          <label>ZAI_API_KEY</label>
          <input type="password" class="tool-input" id="key-zai" placeholder="z-ai-key-...">
        </div>
        <div class="setting-group">
          <label>OPENCODE_API_KEY</label>
          <input type="password" class="tool-input" id="key-opencode" placeholder="oc-key-...">
        </div>
      </div>

      <div class="settings-footer">
        <button class="btn-modal btn-modal-cancel" id="btn-settings-load">
          Load from Trueox
        </button>
        <button class="btn-modal btn-modal-save" id="btn-settings-save">
          Save Settings
        </button>
      </div>
    </div>
  </div>

  <!-- Toast Toast Container -->
  <div class="toast-container" id="toast-container"></div>

  <script>
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
    function initConversations() {
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
  </script>
</body>
</html>`
