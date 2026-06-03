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
    runCount: 0,
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

  function formatTokens(n) {
    if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
    if (n >= 1000) return (n / 1000).toFixed(1) + 'k';
    return '' + n;
  }

  function formatCost(v) {
    var s = v.toFixed(2);
    if (s === '0.00') s = v.toFixed(4);
    return '$' + s;
  }

  function formatUsage(usage) {
    if (!usage) return '';
    if (usage.cost_usd) {
      return formatCost(usage.cost_usd);
    }
    return '';
  }

  function usageNeedsRefresh(usage) {
    if (!usage) return false;
    return usage.cache_read_input_tokens === undefined ||
      usage.cache_creation_input_tokens === undefined;
  }

  async function refreshUsageFromTranscript(taskName, usage) {
    try {
      const resp = await fetch('/tasks/' + taskName + '/transcript.jsonl');
      if (!resp.ok) return usage;
      const text = await resp.text();
      const lines = text.split('\n');

      let totalInput = 0, totalOutput = 0;
      let cacheRead = 0, cacheCreation = 0;
      let costUsd = 0;
      let hasUsage = false;

      for (const line of lines) {
        if (!line.trim()) continue;
        let entry;
        try { entry = JSON.parse(line); } catch (_) { continue; }
        if (entry.type !== 'result') continue;
        if (entry.total_cost_usd) costUsd += entry.total_cost_usd;
        if (entry.usage) {
          hasUsage = true;
          totalInput += entry.usage.input_tokens || 0;
          totalOutput += entry.usage.output_tokens || 0;
          cacheRead += entry.usage.cache_read_input_tokens || 0;
          cacheCreation += entry.usage.cache_creation_input_tokens || 0;
        }
      }

      if (!hasUsage) return usage;

      return {
        input_tokens: totalInput,
        output_tokens: totalOutput,
        cache_read_input_tokens: cacheRead,
        cache_creation_input_tokens: cacheCreation,
        cost_usd: costUsd || usage.cost_usd,
      };
    } catch (_) {
      return usage;
    }
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
    if (!resp.ok) return { name, description: '', created_at: '', updated_at: '', author: '', source: null, prs: [], status: '', sandbox_destroyed: false };
    const d = await resp.json();
    return {
      name: d.name || name,
      description: d.description || '',
      created_at: d.created_at || '',
      updated_at: d.updated_at || '',
      author: d.author || '',
      source: d.source || null,
      prs: (d.resources && d.resources.github && d.resources.github.prs) || [],
      status: d.status || '',
      sandbox_destroyed: !!d.sandbox_destroyed,
      usage: d.usage || null,
    };
  }

  function computeStatus(task) {
    if (task.status) return task.status;
    const hasOpen = (task.prs || []).some((pr) => !pr.closed);
    if (hasOpen) return 'waiting';
    return 'done';
  }

  function statusBadge(task) {
    const s = computeStatus(task);
    const labels = { in_progress: 'in progress', waiting: 'waiting', done: 'done' };
    const label = labels[s] || s;
    return '<span class="status-badge status-' + s + '">' + escapeHtml(label) + '</span>';
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

  const STATUS_LANES = [
    { key: 'in_progress', label: 'in progress' },
    { key: 'waiting', label: 'waiting for review' },
    { key: 'done', label: 'done' },
  ];

  function renderTaskList() {
    const container = $('#task-list');
    container.innerHTML = '';

    const all = [...state.tasks.values()];

    if (all.length === 0) {
      container.innerHTML = '<div class="loading">no tasks found<span class="blink">_</span></div>';
      return;
    }

    const groups = { in_progress: [], waiting: [], done: [] };
    for (const task of all) {
      const s = computeStatus(task);
      (groups[s] || groups.done).push(task);
    }

    for (const lane of STATUS_LANES) {
      const tasks = groups[lane.key];
      if (tasks.length === 0) continue;

      tasks.sort((a, b) =>
        new Date(b.updated_at || b.created_at || 0) - new Date(a.updated_at || a.created_at || 0)
      );

      const section = document.createElement('div');
      section.className = 'status-lane status-lane-' + lane.key;
      section.innerHTML = '<div class="status-lane-header">' + escapeHtml(lane.label) + '</div>';

      const grid = document.createElement('div');
      grid.className = 'status-lane-grid';

      for (const task of tasks) {
        grid.appendChild(renderTaskCard(task));
      }

      section.appendChild(grid);
      container.appendChild(section);
    }
  }

  function renderTaskCard(task) {
    const prs = (task.prs || [])
      .map((pr) => {
        const cls = pr.merged ? 'pr-merged' : pr.closed ? 'pr-closed' : 'pr-open';
        return '<a href="' + escapeHtml(pr.url) + '" target="_blank" class="pr-badge ' + cls +
          '" onclick="event.stopPropagation()">PR #' + pr.number + ' ' + escapeHtml(pr.repo) + '</a>';
      })
      .join('');

    const isRunning = computeStatus(task) === 'in_progress';
    const card = document.createElement('div');
    card.className = 'task-card' + (isRunning ? ' task-card-running' : '');
    card.setAttribute('data-task', task.name);
    const usageStr = formatUsage(task.usage);
    const usageHtml = usageStr
      ? '<span class="task-usage">' + escapeHtml(usageStr) + '</span>'
      : '';

    card.innerHTML =
      '<div class="task-header">' +
        '<div class="task-name">' + escapeHtml(task.name) + '</div>' +
        statusBadge(task) +
      '</div>' +
      '<div class="task-desc">' + escapeHtml(task.description) + '</div>' +
      '<div class="task-footer">' +
        '<span class="task-time">' + timeAgo(task.updated_at || task.created_at) + '</span>' +
        usageHtml +
        prs +
      '</div>';
    card.addEventListener('click', () => {
      window.location.hash = 'task/' + task.name;
    });
    return card;
  }

  // ── Render: task detail ──

  function renderTaskMeta(task) {
    $('#detail-task-name').textContent = task.name;

    const prs = (task.prs || [])
      .map((pr) => {
        const cls = pr.merged ? 'pr-merged' : pr.closed ? 'pr-closed' : 'pr-open';
        return '<a href="' + escapeHtml(pr.url) + '" target="_blank" class="pr-badge ' + cls +
          '">PR #' + pr.number + ' ' + escapeHtml(pr.repo) + '</a>';
      })
      .join('');

    let html =
      '<div><span class="meta-label">status:</span>' + statusBadge(task) + '</div>' +
      '<div><span class="meta-label">created:</span><span class="task-time">' +
      timeAgo(task.created_at) + ' (' + escapeHtml(task.created_at || '') + ')</span></div>' +
      '<div><span class="meta-label">updated:</span><span class="task-time">' +
      timeAgo(task.updated_at || task.created_at) + ' (' + escapeHtml(task.updated_at || task.created_at || '') + ')</span></div>';

    if (task.source) {
      const sourceURL = task.source.url ||
        (task.source.tasks_repo && task.source.issue_number
          ? 'https://github.com/' + task.source.tasks_repo + '/issues/' + task.source.issue_number
          : '');
      if (sourceURL) {
        const sourceLabel = task.source.tasks_repo && task.source.issue_number
          ? task.source.tasks_repo + '#' + task.source.issue_number
          : sourceURL;
        html += '<div><span class="meta-label">source:</span><a href="' + escapeHtml(sourceURL) +
          '" target="_blank" rel="noopener">' + escapeHtml(sourceLabel) + '</a></div>';
      }
    }

    if (task.author) {
      html += '<div><span class="meta-label">author:</span>' + escapeHtml(task.author) + '</div>';
    }

    if (task.usage) {
      let tokenParts = [];
      if (task.usage.cache_read_input_tokens) {
        tokenParts.push('<span title="Cache read input tokens">' +
          escapeHtml(formatTokens(task.usage.cache_read_input_tokens)) + '↺</span>');
      }
      if (task.usage.cache_creation_input_tokens) {
        tokenParts.push('<span title="Cache creation input tokens (cache write)">' +
          escapeHtml(formatTokens(task.usage.cache_creation_input_tokens)) + '⊕</span>');
      }
      if (task.usage.input_tokens) {
        tokenParts.push('<span title="Input tokens (non-cache)">' +
          escapeHtml(formatTokens(task.usage.input_tokens)) + '↑</span>');
      }
      if (task.usage.output_tokens) {
        tokenParts.push('<span title="Output tokens">' +
          escapeHtml(formatTokens(task.usage.output_tokens)) + '↓</span>');
      }
      let allParts = [];
      if (tokenParts.length > 0) allParts.push(tokenParts.join(' '));
      if (task.usage.cost_usd) {
        allParts.push('<span title="Total cost (USD)">' + formatCost(task.usage.cost_usd) + '</span>');
      }
      if (allParts.length > 0) {
        html += '<div><span class="meta-label">usage:</span><span class="meta-usage">' +
          allParts.join(' · ') + '</span></div>';
      }
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
    if (entry.type === 'trigger') {
      return renderTrigger(entry);
    }
    if (entry.type === 'system' && entry.subtype === 'init') {
      return renderSystemInit(entry);
    }
    if (entry.type === 'assistant' && entry.message) {
      return renderAssistantContent(entry);
    }
    if (entry.type === 'user' && entry.message) {
      return renderUserResult(entry);
    }
    if (entry.type === 'result') {
      return renderResult(entry);
    }
    if (entry.type === 'task_started' || entry.type === 'task_progress') {
      return renderSubagent(entry);
    }
    return null;
  }

  function renderSystemInit(entry) {
    state.runCount++;
    const runId = 'run-' + state.runCount;

    const div = document.createElement('div');
    div.className = 'entry entry-system';
    div.id = runId;
    div.innerHTML =
      '<span><span class="sys-label">model:</span> ' + escapeHtml(entry.model || '') + '</span>' +
      '<span><span class="sys-label">tools:</span> ' + (entry.tools ? entry.tools.length : 0) + '</span>' +
      '<span><span class="sys-label">session:</span> ' + escapeHtml(truncate(entry.session_id || '', 12)) + '</span>' +
      '<span><span class="sys-label">version:</span> ' + escapeHtml(entry.claude_code_version || '') + '</span>';

    addRunNavLink(runId, state.runCount);
    return div;
  }

  function addRunNavLink(runId, runNumber) {
    const nav = $('#run-nav');
    if (!nav) return;
    const a = document.createElement('a');
    a.href = '#' + runId;
    a.innerHTML = '<span class="run-label">' + runNumber + '</span> ' +
      (runNumber === 1 ? 'initial' : 'update');
    a.addEventListener('click', function (e) {
      e.preventDefault();
      const target = document.getElementById(runId);
      if (target) {
        target.scrollIntoView({ behavior: 'smooth', block: 'start' });
        history.replaceState(null, '', window.location.pathname + window.location.hash.split('#' + 'run-')[0] + '#' + runId);
      }
    });
    nav.appendChild(a);
  }

  function renderResult(entry) {
    const div = document.createElement('div');
    div.className = 'entry entry-run-result';

    const parts = [];
    const subtype = entry.subtype || 'done';
    parts.push(subtype);

    if (entry.num_turns) parts.push(entry.num_turns + ' turns');
    if (entry.duration_ms) parts.push((entry.duration_ms / 1000).toFixed(1) + 's');
    if (entry.total_cost_usd) parts.push(formatCost(entry.total_cost_usd));
    if (entry.usage) {
      const input = entry.usage.input_tokens || 0;
      const output = entry.usage.output_tokens || 0;
      const cacheRead = entry.usage.cache_read_input_tokens || 0;
      const cacheCreate = entry.usage.cache_creation_input_tokens || 0;
      if (input || output || cacheRead || cacheCreate) {
        let tokenParts = [];
        if (cacheRead) tokenParts.push(formatTokens(cacheRead) + '↺');
        if (cacheCreate) tokenParts.push(formatTokens(cacheCreate) + '⊕');
        tokenParts.push(formatTokens(input) + '↑');
        tokenParts.push(formatTokens(output) + '↓');
        parts.push(tokenParts.join(' '));
      }
    }

    div.innerHTML =
      '<span class="result-label">result:</span> ' + escapeHtml(parts.join(' · '));
    return div;
  }

  function renderTrigger(entry) {
    const comments = entry.comments || [];
    if (comments.length === 0) return null;

    const div = document.createElement('div');
    div.className = 'entry entry-trigger';

    let html = '<div class="trigger-header">triggered by PR comment' +
      (comments.length > 1 ? 's' : '') + '</div>';
    for (const c of comments) {
      html += '<div class="trigger-comment">';
      html += '<div class="trigger-meta">';
      html += '@' + escapeHtml(c.user || '');
      if (c.created_at) html += ' at ' + escapeHtml(c.created_at);
      if (c.path) html += ' on ' + escapeHtml(c.path);
      if (c.html_url) {
        html += ' <a href="' + escapeHtml(c.html_url) + '" target="_blank" rel="noopener">[view]</a>';
      }
      html += '</div>';
      if (c.body) {
        html += '<div class="trigger-body">' + escapeHtml(truncate(c.body, 200)) + '</div>';
      }
      html += '</div>';
    }
    div.innerHTML = html;
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
    state.runCount = 0;
    stopPolling('transcript');

    $('#task-list').classList.remove('hidden');
    $('#task-detail').classList.add('hidden');

    await refreshTaskList();
    startPolling('taskList', refreshTaskList, TASK_LIST_POLL_MS);
  }

  async function showTaskDetail(taskName) {
    state.currentTask = taskName;
    state.renderedCount = 0;
    state.runCount = 0;
    stopPolling('taskList');

    $('#task-list').classList.add('hidden');
    $('#task-detail').classList.remove('hidden');
    $('#transcript').innerHTML = '';
    $('#run-nav').innerHTML = '';
    $('#transcript-loading').classList.remove('hidden');

    const meta = state.tasks.get(taskName) || (await fetchTaskMeta(taskName));
    if (usageNeedsRefresh(meta.usage)) {
      meta.usage = await refreshUsageFromTranscript(taskName, meta.usage);
    }
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

  // ── Mock mode ──

  function isMockMode() {
    return new URLSearchParams(window.location.search).has('mock');
  }

  async function loadMockTasks() {
    const resp = await fetch('mock.json');
    if (!resp.ok) throw new Error('Failed to load mock.json: ' + resp.status);
    return resp.json();
  }

  // ── Refresh ──

  async function refreshTaskList() {
    try {
      let metas;
      if (isMockMode()) {
        metas = await loadMockTasks();
      } else {
        const names = await discoverTasks();
        metas = await Promise.all(names.map(fetchTaskMeta));
      }
      state.tasks.clear();
      for (const m of metas) state.tasks.set(m.name, m);
      if (!state.currentTask) renderTaskList();
      setStatus(metas.length + ' tasks | ' + new Date().toLocaleTimeString());
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

  // ── Version footer ──

  async function loadVersionFooter() {
    try {
      const resp = await fetch('/version.json');
      if (!resp.ok) return;
      const info = await resp.json();
      const components = info.components || {};
      const parts = Object.entries(components).map(function ([name, comp]) {
        var label = escapeHtml(name) + ':';
        var commitText = escapeHtml(comp.commit || 'dev');
        var value;
        if (comp.repo && comp.commit) {
          var href = 'https://github.com/' + escapeHtml(comp.repo) + '/commit/' + commitText;
          value = '<a href="' + href + '" target="_blank" rel="noopener" class="version-value">' + commitText + '</a>';
        } else {
          value = '<span class="version-value">' + commitText + '</span>';
        }
        return '<span class="version-component">' + label + ' ' + value + '</span>';
      });
      if (parts.length > 0) {
        $('#version-footer').innerHTML = parts.join('');
      }
    } catch (_) {
      // version info is best-effort
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

    loadVersionFooter();
    handleRoute();
  }

  document.addEventListener('DOMContentLoaded', init);
})();
