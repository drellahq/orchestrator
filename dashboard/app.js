(function () {
  'use strict';

  // ── Configuration ──

  const TASK_LIST_POLL_MS = 30000;
  const TRANSCRIPT_POLL_MS = 5000;
  const RESULT_TRUNCATE_LINES = 15;

  // ── State ──

  const state = {
    tasks: new Map(),
    currentTask: null,
    transcriptLoader: null,
    renderedCount: 0,
    pollTimers: { taskList: null, transcript: null },
  };

  // ── DOM helpers ──

  const $ = (sel) => document.querySelector(sel);
  const $$ = (sel) => document.querySelectorAll(sel);

  function escapeHtml(str) {
    if (!str) return '';
    const el = document.createElement('span');
    el.textContent = str;
    return el.innerHTML;
  }

  function truncate(str, len) {
    if (!str) return '';
    return str.length > len ? str.slice(0, len) + '\u2026' : str;
  }

  function timeAgo(iso) {
    if (!iso) return '';
    const secs = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
    if (secs < 60) return 'just now';
    const mins = Math.floor(secs / 60);
    if (mins < 60) return `${mins}m ago`;
    const hrs = Math.floor(mins / 60);
    if (hrs < 24) return `${hrs}h ago`;
    const days = Math.floor(hrs / 24);
    return `${days}d ago`;
  }

  function showToast(msg) {
    const el = $('#toast');
    el.textContent = msg;
    el.classList.remove('hidden');
    clearTimeout(el._timer);
    el._timer = setTimeout(() => el.classList.add('hidden'), 4000);
  }

  function setStatus(text) {
    $('#status-text').textContent = text;
  }

  // ── Formatting ──

  function formatText(text) {
    if (!text) return '';
    const parts = text.split(/(```[\s\S]*?```)/);
    let html = '';
    for (const part of parts) {
      if (part.startsWith('```')) {
        const m = part.match(/```\w*\n?([\s\S]*?)```/);
        html += '<pre><code>' + escapeHtml(m ? m[1] : part) + '</code></pre>';
      } else {
        let s = escapeHtml(part);
        s = s.replace(/`([^`]+)`/g, '<code>$1</code>');
        s = s.replace(/(https?:\/\/[^\s&lt;]+)/g, '<a href="$1" target="_blank" rel="noopener">$1</a>');
        s = s.replace(/\n/g, '<br>');
        html += s;
      }
    }
    return html;
  }

  function toolSummary(name, input) {
    if (!input) return '';
    switch (name) {
      case 'Bash': return input.description || truncate(input.command, 100);
      case 'Read': return input.file_path;
      case 'Write': return input.file_path;
      case 'Edit': return input.file_path;
      case 'Glob': return input.pattern;
      case 'Grep': return '/' + (input.pattern || '') + '/ in ' + (input.path || '.');
      case 'WebFetch': return truncate(input.url, 80);
      case 'WebSearch': return input.query;
      case 'TodoWrite': return 'updating todos';
      case 'Task':
      case 'Agent': return input.description || truncate(input.prompt, 80);
      case 'ToolSearch': return truncate(input.query, 80);
      default:
        if (name.startsWith('mcp__')) {
          const parts = name.split('__');
          return parts.slice(1).join('.');
        }
        return truncate(JSON.stringify(input), 80);
    }
  }

  // ── API: task discovery ──

  async function discoverTasks() {
    const resp = await fetch('/tasks/', {
      headers: { 'Accept': 'application/json' },
    });
    if (!resp.ok) throw new Error('Failed to list tasks: ' + resp.status);
    const data = await resp.json();
    const items = Array.isArray(data) ? data : (data.items || []);
    return items
      .filter((e) => e.is_dir || e.IsDir)
      .map((e) => (e.name || e.Name || '').replace(/\/$/, ''));
  }

  async function fetchTaskMeta(name) {
    let resp = await fetch('/tasks/' + name + '/state.json');
    if (!resp.ok) resp = await fetch('/tasks/' + name + '/metadata.json');
    if (!resp.ok) return { name, description: '', created_at: '', author: '', prs: [] };
    const d = await resp.json();
    return {
      name: d.name || name,
      description: d.description || '',
      created_at: d.created_at || '',
      updated_at: d.updated_at || '',
      author: d.author || '',
      status: d.status || '',
      prs: (d.resources && d.resources.github && d.resources.github.prs) || [],
    };
  }

  // ── API: transcript loader ──

  class TranscriptLoader {
    constructor(taskName) {
      this.taskName = taskName;
      this.bytesLoaded = 0;
      this.buffer = '';
      this.entries = [];
    }

    async fetch() {
      const url = '/tasks/' + this.taskName + '/transcript.jsonl';
      const headers = {};
      if (this.bytesLoaded > 0) {
        headers['Range'] = 'bytes=' + this.bytesLoaded + '-';
      }

      let resp;
      try {
        resp = await fetch(url, { headers });
      } catch (e) {
        return [];
      }

      if (resp.status === 416) return [];

      const buf = await resp.arrayBuffer();
      const text = new TextDecoder().decode(buf);

      if (resp.status === 200) {
        if (this.bytesLoaded > 0) {
          this.buffer = '';
          this.entries = [];
        }
        this.bytesLoaded = buf.byteLength;
      } else if (resp.status === 206) {
        this.bytesLoaded += buf.byteLength;
      }

      return this._parseChunk(text);
    }

    _parseChunk(text) {
      const input = this.buffer + text;
      const lines = input.split('\n');
      this.buffer = '';
      const out = [];

      for (let i = 0; i < lines.length; i++) {
        const line = lines[i].trim();
        if (!line) continue;
        try {
          out.push(JSON.parse(line));
        } catch (_) {
          if (i === lines.length - 1) {
            this.buffer = lines[i];
          }
        }
      }

      this.entries.push(...out);
      return out;
    }
  }

  // ── Render: task list ──

  function renderTaskList() {
    const container = $('#task-list');
    container.innerHTML = '';

    const sorted = [...state.tasks.values()].sort((a, b) => {
      return new Date(b.updated_at || b.created_at || 0) - new Date(a.updated_at || a.created_at || 0);
    });

    if (sorted.length === 0) {
      container.innerHTML = '<div class="loading">no tasks found<span class="blink">_</span></div>';
      return;
    }

    for (const task of sorted) {
      const prs = (task.prs || [])
        .map((pr) => {
          const cls = pr.closed ? 'pr-closed' : 'pr-open';
          return '<a href="' + escapeHtml(pr.url) + '" target="_blank" class="pr-badge ' + cls +
            '" onclick="event.stopPropagation()">PR #' + pr.number + ' ' + escapeHtml(pr.repo) + '</a>';
        })
        .join('');

      const statusBadge = task.status
        ? '<span class="status-badge status-' + escapeHtml(task.status) + '">' + escapeHtml(task.status) + '</span>'
        : '';

      const card = document.createElement('div');
      card.className = 'task-card';
      card.setAttribute('data-task', task.name);
      card.innerHTML =
        '<div class="task-header">' +
          '<div class="task-name">' + escapeHtml(task.name) + '</div>' +
          statusBadge +
        '</div>' +
        '<div class="task-desc">' + escapeHtml(task.description) + '</div>' +
        '<div class="task-footer">' +
          '<span class="task-time">' + timeAgo(task.updated_at || task.created_at) + '</span>' +
          prs +
        '</div>';
      card.addEventListener('click', () => {
        window.location.hash = 'task/' + task.name;
      });
      container.appendChild(card);
    }
  }

  // ── Render: task detail ──

  function renderTaskMeta(task) {
    $('#detail-task-name').textContent = task.name;

    const prs = (task.prs || [])
      .map((pr) => {
        const cls = pr.closed ? 'pr-closed' : 'pr-open';
        return '<a href="' + escapeHtml(pr.url) + '" target="_blank" class="pr-badge ' + cls +
          '">PR #' + pr.number + ' ' + escapeHtml(pr.repo) + '</a>';
      })
      .join('');

    let html =
      '<div><span class="meta-label">status:</span>' +
      (task.status
        ? '<span class="status-badge status-' + escapeHtml(task.status) + '">' + escapeHtml(task.status) + '</span>'
        : '<span class="task-time">unknown</span>') +
      '</div>' +
      '<div><span class="meta-label">created:</span><span class="task-time">' +
      timeAgo(task.created_at) + ' (' + escapeHtml(task.created_at || '') + ')</span></div>' +
      '<div><span class="meta-label">updated:</span><span class="task-time">' +
      timeAgo(task.updated_at || task.created_at) + ' (' + escapeHtml(task.updated_at || task.created_at || '') + ')</span></div>';

    if (task.author) {
      html += '<div><span class="meta-label">author:</span>' + escapeHtml(task.author) + '</div>';
    }

    if (prs) {
      html += '<div class="meta-prs">' + prs + '</div>';
    }

    html += '<div class="meta-desc">' + escapeHtml(task.description) + '</div>';

    $('#task-meta').innerHTML = html;
  }

  // ── Render: transcript entries ──

  function renderEntries(entries) {
    const container = $('#transcript');
    const atBottom = isScrolledToBottom();

    for (const entry of entries) {
      const el = renderEntry(entry);
      if (el) container.appendChild(el);
    }

    if (atBottom) scrollToBottom();
  }

  function isScrolledToBottom() {
    const margin = 80;
    return (window.innerHeight + window.scrollY) >= (document.body.scrollHeight - margin);
  }

  function scrollToBottom() {
    window.scrollTo({ top: document.body.scrollHeight, behavior: 'smooth' });
  }

  function renderEntry(entry) {
    if (entry.type === 'system' && entry.subtype === 'init') {
      return renderSystemInit(entry);
    }
    if (entry.type === 'assistant' && entry.message) {
      return renderAssistantContent(entry);
    }
    if (entry.type === 'user' && entry.message) {
      return renderUserResult(entry);
    }
    if (entry.type === 'task_started' || entry.type === 'task_progress') {
      return renderSubagent(entry);
    }
    return null;
  }

  function renderSystemInit(entry) {
    const div = document.createElement('div');
    div.className = 'entry entry-system';
    div.innerHTML =
      '<span><span class="sys-label">model:</span> ' + escapeHtml(entry.model || '') + '</span>' +
      '<span><span class="sys-label">tools:</span> ' + (entry.tools ? entry.tools.length : 0) + '</span>' +
      '<span><span class="sys-label">session:</span> ' + escapeHtml(truncate(entry.session_id || '', 12)) + '</span>' +
      '<span><span class="sys-label">version:</span> ' + escapeHtml(entry.claude_code_version || '') + '</span>';
    return div;
  }

  function renderAssistantContent(entry) {
    const contents = entry.message.content || [];
    if (contents.length === 0) return null;

    const frag = document.createDocumentFragment();

    for (const block of contents) {
      if (block.type === 'thinking' && block.thinking) {
        const div = document.createElement('div');
        div.className = 'entry entry-thinking';
        const preview = truncate(block.thinking, 100);
        div.innerHTML =
          '<details><summary>thinking: ' + escapeHtml(preview) + '</summary>' +
          '<div class="thinking-body">' + escapeHtml(block.thinking) + '</div></details>';
        frag.appendChild(div);
      } else if (block.type === 'text' && block.text) {
        const div = document.createElement('div');
        div.className = 'entry entry-text';
        div.innerHTML = formatText(block.text);
        frag.appendChild(div);
      } else if (block.type === 'tool_use') {
        const div = document.createElement('div');
        div.className = 'entry entry-tool';
        const summary = toolSummary(block.name, block.input);
        div.innerHTML =
          '<div class="tool-header">' +
            '<span class="tool-name">' + escapeHtml(block.name || '') + '</span>' +
            '<span class="tool-summary">' + escapeHtml(summary) + '</span>' +
          '</div>' +
          '<pre class="tool-input">' + escapeHtml(JSON.stringify(block.input, null, 2)) + '</pre>';
        div.addEventListener('click', () => div.classList.toggle('expanded'));
        frag.appendChild(div);
      }
    }

    return frag.children.length > 0 ? frag : null;
  }

  function renderUserResult(entry) {
    const contents = entry.message.content || [];
    if (typeof contents === 'string') {
      return makeResultDiv(contents, false);
    }

    const frag = document.createDocumentFragment();
    for (const block of contents) {
      if (block.type === 'tool_result') {
        let text = '';
        if (typeof block.content === 'string') {
          text = block.content;
        } else if (Array.isArray(block.content)) {
          text = block.content.map((c) => c.text || c.tool_name || JSON.stringify(c)).join('\n');
        }

        if (entry.tool_use_result) {
          const r = entry.tool_use_result;
          if (typeof r === 'object' && r.stdout !== undefined) {
            text = r.stdout || text;
            if (r.stderr) text += '\nSTDERR: ' + r.stderr;
          } else if (typeof r === 'string') {
            text = r;
          }
        }

        frag.appendChild(makeResultDiv(text, !!block.is_error));
      }
    }
    return frag.children.length > 0 ? frag : null;
  }

  function makeResultDiv(text, isError) {
    const div = document.createElement('div');
    div.className = 'entry entry-result' + (isError ? ' is-error' : '');

    const lines = (text || '').split('\n');
    if (lines.length > RESULT_TRUNCATE_LINES) {
      const shown = lines.slice(0, RESULT_TRUNCATE_LINES).join('\n');
      const full = text;
      div.innerHTML =
        '<div class="result-output">' + escapeHtml(shown) + '</div>' +
        '<div class="result-truncated">[' + (lines.length - RESULT_TRUNCATE_LINES) + ' more lines - click to expand]</div>';
      div.querySelector('.result-truncated').addEventListener('click', (e) => {
        e.stopPropagation();
        div.querySelector('.result-output').textContent = full;
        div.querySelector('.result-truncated').remove();
      });
    } else {
      div.innerHTML = '<div class="result-output">' + escapeHtml(text || '') + '</div>';
    }

    return div;
  }

  function renderSubagent(entry) {
    const div = document.createElement('div');
    div.className = 'entry entry-subagent';
    if (entry.type === 'task_started') {
      div.textContent = '>> sub-agent started: ' + (entry.task_description || entry.description || '');
    } else {
      div.textContent = '>> sub-agent progress';
    }
    return div;
  }

  // ── Views ──

  async function showTaskList() {
    state.currentTask = null;
    state.transcriptLoader = null;
    state.renderedCount = 0;
    stopPolling('transcript');

    $('#task-list').classList.remove('hidden');
    $('#task-detail').classList.add('hidden');

    await refreshTaskList();
    startPolling('taskList', refreshTaskList, TASK_LIST_POLL_MS);
  }

  async function showTaskDetail(taskName) {
    state.currentTask = taskName;
    state.renderedCount = 0;
    stopPolling('taskList');

    $('#task-list').classList.add('hidden');
    $('#task-detail').classList.remove('hidden');
    $('#transcript').innerHTML = '';
    $('#transcript-loading').classList.remove('hidden');

    const meta = state.tasks.get(taskName) || (await fetchTaskMeta(taskName));
    renderTaskMeta(meta);

    state.transcriptLoader = new TranscriptLoader(taskName);

    try {
      const entries = await state.transcriptLoader.fetch();
      $('#transcript-loading').classList.add('hidden');
      renderEntries(entries);
    } catch (e) {
      $('#transcript-loading').textContent = 'failed to load transcript';
      showToast('Error loading transcript: ' + e.message);
    }

    startPolling('transcript', refreshTranscript, TRANSCRIPT_POLL_MS);
  }

  // ── Refresh ──

  async function refreshTaskList() {
    try {
      const names = await discoverTasks();
      const metas = await Promise.all(names.map(fetchTaskMeta));
      state.tasks.clear();
      for (const m of metas) state.tasks.set(m.name, m);
      if (!state.currentTask) renderTaskList();
      setStatus(names.length + ' tasks | ' + new Date().toLocaleTimeString());
    } catch (e) {
      showToast('Error refreshing: ' + e.message);
    }
  }

  async function refreshTranscript() {
    if (!state.transcriptLoader) return;
    try {
      const newEntries = await state.transcriptLoader.fetch();
      if (newEntries.length > 0) {
        renderEntries(newEntries);
        setStatus('live | ' + new Date().toLocaleTimeString());
      }
    } catch (_) {
      // silent retry on next poll
    }
  }

  // ── Polling ──

  function startPolling(key, fn, ms) {
    stopPolling(key);
    state.pollTimers[key] = setInterval(fn, ms);
  }

  function stopPolling(key) {
    if (state.pollTimers[key]) {
      clearInterval(state.pollTimers[key]);
      state.pollTimers[key] = null;
    }
  }

  // ── Router ──

  function handleRoute() {
    const hash = window.location.hash.slice(1);
    if (hash.startsWith('task/')) {
      showTaskDetail(decodeURIComponent(hash.slice(5)));
    } else {
      showTaskList();
    }
  }

  // ── Init ──

  function init() {
    window.addEventListener('hashchange', handleRoute);

    $('#refresh-btn').addEventListener('click', () => {
      if (state.currentTask) refreshTranscript();
      else refreshTaskList();
    });

    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && state.currentTask) {
        window.location.hash = '';
      }
      if (e.key === 'r' && !e.ctrlKey && !e.metaKey && document.activeElement === document.body) {
        if (state.currentTask) refreshTranscript();
        else refreshTaskList();
      }
    });

    handleRoute();
  }

  document.addEventListener('DOMContentLoaded', init);
})();
