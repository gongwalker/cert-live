(function () {
  'use strict';

  // ===== 状态 =====
  var state = {
    domains: [],
    search: '',
    filterTagIDs: {},     // 多标签 AND 筛选
    filterExpiringSoon: false,  // 10 天内到期快捷筛选
    sortMode: 'manual',   // manual | expiry_asc | expiry_desc
    checkingIds: {} // 正在检测中的域名 id
  };

  // ===== DOM 引用 =====
  var $body = document.getElementById('domainsBody');
  var $cardList = document.getElementById('cardList');
  var $empty = document.getElementById('emptyState');
  var $count = document.getElementById('countBadge');
  var $search = document.getElementById('searchInput');
  var $toast = document.getElementById('toast');

  // 表单弹窗
  var $form = document.getElementById('domainForm');
  var $formTitle = document.getElementById('domainModalTitle');
  var $formSub = document.getElementById('domainModalSub');
  var $formId = document.getElementById('domainId');
  var $formHost = document.getElementById('hostInput');
  var $formNotes = document.getElementById('notesInput');
  var $formSubmit = document.getElementById('domainSubmitBtn');
  var $tagPicker = document.getElementById('tagPicker');
  var allTags = [];           // 全部可用标签（设置里管理的）
  var selectedTagIDs = {};    // 当前表单选中的 tag id → true
  var $notesCounter = document.getElementById('notesCounter');
  var NOTES_MAX = 120;

  function updateNotesCount() {
    var len = $formNotes.value.length;
    $notesCounter.textContent = len + ' / ' + NOTES_MAX;
    $notesCounter.classList.toggle('warn', len >= NOTES_MAX);
  }
  $formNotes.addEventListener('input', updateNotesCount);

  // 删除弹窗
  var $deleteHost = document.getElementById('deleteHostName');
  var $deleteConfirm = document.getElementById('deleteConfirmBtn');
  var pendingDeleteId = null;

  // ===== 工具函数 =====
  function escapeHTML(s) {
    if (s == null) return '';
    return String(s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  // 归一化主机名：去掉 scheme、path、查询串、首尾空白
  // 支持 "https://example.com:8443/path" → "example.com:8443"
  function normalizeHost(input) {
    var s = (input || '').trim();
    if (!s) return '';
    s = s.replace(/^[a-zA-Z]+:\/\//, ''); // scheme
    s = s.replace(/[/?#].*$/, '');         // path / query / fragment
    return s.trim();
  }

  function fmtDate(unix) {
    if (!unix) return '<span class="cell-muted">—</span>';
    var d = new Date(unix * 1000);
    var pad = function (n) { return n < 10 ? '0' + n : n; };
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) +
      ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
  }

  function fmtRelative(unix) {
    if (!unix) return '从未';
    var diff = Date.now() / 1000 - unix;
    if (diff < 60) return '刚刚';
    if (diff < 3600) return Math.floor(diff / 60) + ' 分钟前';
    if (diff < 86400) return Math.floor(diff / 3600) + ' 小时前';
    if (diff < 86400 * 30) return Math.floor(diff / 86400) + ' 天前';
    return fmtDate(unix);
  }

  // 计算证书状态：返回 {cls, label}
  // 全部加「证书」前缀，与 HTTP 徽章对仗
  function statusOf(d) {
    if (d.last_error) return { cls: 'badge-err', label: '证书失败' };
    if (!d.not_after) return { cls: 'badge-err', label: '证书未检测' };
    if (d.days_remaining < 0) return { cls: 'badge-danger', label: '证书已过期' };
    if (d.days_remaining <= 7) return { cls: 'badge-danger', label: '证书紧急' };
    if (d.days_remaining <= 30) return { cls: 'badge-warn', label: '证书将过期' };
    return { cls: 'badge-ok', label: '证书健康' };
  }

  function daysBadge(days) {
    var cls = 'days-ok', text;
    if (days < 0) { cls = 'days-danger'; text = '已过期 ' + Math.abs(days) + ' 天'; }
    else if (days <= 7) { cls = 'days-danger'; text = '剩 ' + days + ' 天'; }
    else if (days <= 30) { cls = 'days-warn'; text = '剩 ' + days + ' 天'; }
    else { text = '剩 ' + days + ' 天'; }
    return '<span class="expiry-days ' + cls + '">' + text + '</span>';
  }

  // hex → rgba 字符串（用于把 tag 颜色变浅做背景/边框）
  function hexToRgba(hex, alpha) {
    if (!hex || hex[0] !== '#' || hex.length < 7) return '';
    var r = parseInt(hex.slice(1, 3), 16);
    var g = parseInt(hex.slice(3, 5), 16);
    var b = parseInt(hex.slice(5, 7), 16);
    return 'rgba(' + r + ',' + g + ',' + b + ',' + alpha + ')';
  }

  // 渲染单个标签 chip：带颜色（无颜色时回退默认蓝，由 CSS 控制）
  // opts: {cls: 自定义类, active: 是否高亮（高亮时强制主题蓝）, withIcon: 是否带图标}
  function tagChip(t, opts) {
    opts = opts || {};
    var cls = opts.cls || 'domain-tag';
    var iconHTML = (t.icon && opts.withIcon !== false) ? '<i class="fas ' + escapeHTML(t.icon) + '"></i>' : '';
    var style = '';
    if (t.color && !opts.active) {
      // 用 tag 自身颜色：背景 15%、边框 45%、文字纯色
      style = ' style="background:' + hexToRgba(t.color, 0.15) + ';border-color:' +
              hexToRgba(t.color, 0.5) + ';color:' + escapeHTML(t.color) + ';"';
    }
    return '<span class="' + cls + '"' + style + '>' + iconHTML + escapeHTML(t.name) + '</span>';
  }

  // 渲染 HTTP 状态码徽章：2xx/3xx 绿、4xx 黄、5xx 红、连接失败 灰红、未检测 不显示
  function httpBadgeHTML(d) {
    if (!d.http_checked) return '';  // 从未检测过
    if (d.http_error) {
      return '<span class="http-badge http-err" title="' + escapeHTML(d.http_error) + '"><i class="fas fa-unlink"></i> HTTP 失败</span>';
    }
    var code = d.http_status;
    if (!code) return '';
    var cls;
    if (code >= 200 && code < 400) cls = 'http-ok';      // 2xx/3xx 健康
    else if (code >= 400 && code < 500) cls = 'http-warn'; // 4xx 客户端错误
    else if (code >= 500) cls = 'http-err';                // 5xx 服务端错误
    else cls = 'http-err';
    return '<span class="http-badge ' + cls + '" title="HTTP 状态码">HTTP ' + code + '</span>';
  }

  function toast(msg, type) {
    var icon = type === 'error' ? '<i class="fas fa-circle-exclamation"></i>'
             : type === 'success' ? '<i class="fas fa-circle-check"></i>'
             : '<i class="fas fa-circle-info"></i>';
    $toast.innerHTML = icon + '<span>' + escapeHTML(msg) + '</span>';
    $toast.className = 'toast show ' + (type || '');
    $toast.hidden = false;
    clearTimeout(toast._t);
    toast._t = setTimeout(function () {
      $toast.className = 'toast ' + (type || '');
      setTimeout(function () { $toast.hidden = true; }, 250);
    }, 2400);
  }

  // ===== API =====
  // 统一处理响应信封：{code, message, data}
  // code !== 200 视为错误，抛出带 message 的 Error
  // 401 自动跳登录
  function api(method, url, body) {
    var opts = { method: method, headers: {}, credentials: 'same-origin' };
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    return fetch(url, opts).then(function (r) {
      if (r.status === 401) { location.href = '/login'; throw new Error('未登录'); }
      return r.json().catch(function () { throw new Error('响应不是合法 JSON'); });
    }).then(function (res) {
      if (!res || typeof res.code === 'undefined') {
        throw new Error('响应格式不正确');
      }
      if (res.code !== 200) {
        throw new Error(res.message || ('错误码 ' + res.code));
      }
      return res.data;
    });
  }

  // ===== 加载 =====
  function loadDomains() {
    var url = '/api/domains?search=' + encodeURIComponent(state.search);
    Object.keys(state.filterTagIDs).forEach(function (id) {
      url += '&tag_ids=' + encodeURIComponent(id);
    });
    $body.innerHTML = '<tr class="loading-row"><td colspan="8"><div class="spinner"></div></td></tr>';
    $empty.hidden = true;
    return api('GET', url).then(function (list) {
      state.domains = list || [];
      render();
    }).catch(function (err) {
      $body.innerHTML = '<tr><td colspan="8" style="padding:30px;text-align:center;color:#ff6b6b;">加载失败：' +
        escapeHTML(err.message) + '</td></tr>';
    });
  }

  // ===== 渲染 =====
  // manual: 用后端 sort_order 返回顺序；expiry_asc/desc: 按到期时间排（错误/未检测放最后）
  function filteredSorted() {
    var list = state.domains.slice();
    if (state.filterExpiringSoon) {
      list = list.filter(function (d) {
        // 排除错误/未检测；剩余天数 0-9（含已过期视为不在此筛选范围）
        return !d.last_error && d.not_after && d.days_remaining >= 0 && d.days_remaining < 10;
      });
    }
    if (state.sortMode === 'expiry_asc' || state.sortMode === 'expiry_desc') {
      var dir = state.sortMode === 'expiry_asc' ? 1 : -1;
      list.sort(function (a, b) {
        var aKey = (a.last_error || !a.not_after) ? null : a.not_after;
        var bKey = (b.last_error || !b.not_after) ? null : b.not_after;
        if (aKey === null && bKey === null) return 0;
        if (aKey === null) return 1;   // 无日期的排到最后
        if (bKey === null) return -1;
        return (aKey - bKey) * dir;
      });
    }
    return list;
  }

  function render() {
    var list = filteredSorted();
    $count.textContent = list.length + ' 条';
    renderTagFilter();  // 同步刷新筛选条（即将到期数量 + 已选标签态）

    if (!list.length) {
      $body.innerHTML = '';
      $cardList.innerHTML = '';
      $empty.hidden = false;
      return;
    }
    $empty.hidden = true;

    // 双视图同步渲染（CSS 决定显示哪个）；无分页，一次渲染全部
    $body.innerHTML = list.map(renderRow).join('');
    $cardList.innerHTML = list.map(renderCard).join('');
  }

  function renderRow(d) {
    var st = statusOf(d);
    var checking = state.checkingIds[d.id];

    // 域名 + 状态徽章 + 标签 + 端口
    // host + 端口 + 路径（非根路径时显示）；URL 解码后展示，失败回退原文
    var portSuffix = d.port && d.port !== 443 ? ':' + d.port : '';
    var pathSuffix = '';
    if (d.path && d.path !== '/' && d.path !== '') {
      var decoded = d.path;
      try { decoded = decodeURIComponent(d.path); } catch (e) {}
      pathSuffix = escapeHTML(decoded);
    }
    var fullHost = escapeHTML(d.host) + (portSuffix ? '<span class="host-port">' + escapeHTML(portSuffix) + '</span>' : '')
                 + (pathSuffix ? '<span class="host-path">' + pathSuffix + '</span>' : '');
    var href = 'https://' + d.host + portSuffix + (d.path && d.path !== '/' ? d.path : '');
    var hostLink = '<a class="host-name" href="' + escapeHTML(href) + '" target="_blank" rel="noopener noreferrer" title="新窗口打开">' + fullHost + '</a>';
    var tagsHTML = (d.tags && d.tags.length)
      ? '<div class="domain-tags">' + d.tags.map(function (t) {
          return tagChip(t);
        }).join('') + '</div>'
      : '';
    var httpBadge = httpBadgeHTML(d);  // HTTP 状态码徽章
    var host = '<td class="col-host"><div class="host-cell">' +
      hostLink +
      '<div class="host-meta-row">' +
        '<span class="badge ' + st.cls + '">' + st.label + '</span>' +
        httpBadge +
        (d.tags && d.tags.length ? d.tags.map(function (t) { return tagChip(t); }).join('') : '') +
      '</div>' +
      '</div></td>';

    // 证书信息（主体 + 序列号 + 签发CA 整合到一格）
    var cert;
    if (d.last_error || !d.subject) {
      var emptyText = d.last_error ? '检测失败' : '尚未检测';
      var errLine = d.last_error
        ? '<span class="cert-error"><i class="fas fa-triangle-exclamation"></i> ' + escapeHTML(d.last_error) + '</span>'
        : '';
      cert = '<td class="col-cert"><div class="cert-info">' +
        '<span class="cert-empty">' + emptyText + '</span>' +
        errLine +
        '</div></td>';
    } else {
      var sansExtra = '';
      if (d.sans && d.sans.length > 1) {
        sansExtra = '<button type="button" class="cert-tag san-trigger" data-san-id="' + d.id + '" title="点击查看完整列表">+' +
          (d.sans.length - 1) + ' SAN</button>';
      }
      var caName = d.issuer_org || d.issuer || '—';
      cert = '<td class="col-cert"><div class="cert-info">' +
        '<div class="cert-line cert-line-main">' +
          '<span class="cert-cn" title="' + escapeHTML(d.subject) + '">' + escapeHTML(d.subject) + '</span>' +
          (d.is_wildcard ? '<span class="cert-tag">泛域名</span>' : '') +
          sansExtra +
        '</div>' +
        '<div class="cert-line cert-line-meta">' +
          '<span class="cert-label">序列号:</span>' +
          '<span class="cert-val cert-serial" title="' + escapeHTML(d.serial_number) + '">' + escapeHTML(d.serial_number) + '</span>' +
        '</div>' +
        '<div class="cert-line cert-line-meta">' +
          '<span class="cert-label">签发CA:</span>' +
          '<span class="cert-val cert-ca" title="' + escapeHTML(caName) + '">' + escapeHTML(caName) + '</span>' +
        '</div>' +
      '</div></td>';
    }

    // 到期时间（两行：日期 + 剩 N 天）
    var expiry;
    if (d.last_error || !d.not_after) {
      expiry = '<td class="col-expiry cell-muted">—</td>';
    } else {
      expiry = '<td class="col-expiry"><div class="cell-time">' +
        '<span class="expiry-date">' + fmtDate(d.not_after) + '</span>' +
        daysBadge(d.days_remaining) +
        '</div></td>';
    }

    // 检测时间
    var checked = '<td class="col-checked"><div class="cell-time">' +
      '<span>' + (d.last_checked ? fmtRelative(d.last_checked) : '<span class="cell-muted">从未</span>') + '</span>' +
      (d.last_checked ? '<span class="cell-muted">' + fmtDate(d.last_checked) + '</span>' : '') +
      '</div></td>';

    // 说明
    var notes = '<td class="col-notes"><span class="notes-cell" title="' + escapeHTML(d.notes || '') + '">' +
      escapeHTML(d.notes || '') + '</span></td>';

    // 操作
    var actions = '<td class="col-actions">' +
      '<button class="action-btn' + (checking ? ' checking' : '') + '" data-action="check" data-id="' + d.id + '" title="立即检测"' + (checking ? ' disabled' : '') + '><i class="fas fa-bolt"></i></button>' +
      '<button class="action-btn" data-action="edit" data-id="' + d.id + '" title="编辑"><i class="fas fa-pen"></i></button>' +
      '<button class="action-btn danger" data-action="delete" data-id="' + d.id + '" title="删除"><i class="fas fa-trash"></i></button>' +
      '</td>';

    // 拖拽手柄（仅在 manual 排序 + 无搜索/筛选 时启用）
    var canDrag = state.sortMode === 'manual'
               && !state.search
               && Object.keys(state.filterTagIDs).length === 0;
    var drag = '<td class="col-drag">' +
      (canDrag ? '<span class="drag-handle" title="拖动排序"><i class="fas fa-grip-vertical"></i></span>' : '') +
      '</td>';

    return '<tr data-id="' + d.id + '"' + (canDrag ? ' draggable="false"' : '') + '>' +
      drag + host + cert + expiry + checked + notes + actions +
      '</tr>';
  }

  // 渲染移动端卡片视图（与 renderRow 共用 helpers，事件用同一 handler）
  function renderCard(d) {
    var st = statusOf(d);
    var checking = state.checkingIds[d.id];

    // 头部：host + 状态 + HTTP 状态 + 操作
    var portSuffix = d.port && d.port !== 443 ? ':' + d.port : '';
    var pathSuffix = '';
    if (d.path && d.path !== '/' && d.path !== '') {
      var decoded = d.path;
      try { decoded = decodeURIComponent(d.path); } catch (e) {}
      pathSuffix = escapeHTML(decoded);
    }
    var fullHost = escapeHTML(d.host) + (portSuffix ? '<span class="host-port">' + escapeHTML(portSuffix) + '</span>' : '')
                 + (pathSuffix ? '<span class="host-path">' + pathSuffix + '</span>' : '');
    var href = 'https://' + d.host + portSuffix + (d.path && d.path !== '/' ? d.path : '');
    var hostLink = '<a class="host-name" href="' + escapeHTML(href) + '" target="_blank" rel="noopener noreferrer" title="新窗口打开">' + fullHost + '</a>';
    var tagsHTML = (d.tags && d.tags.length)
      ? '<div class="domain-tags">' + d.tags.map(function (t) {
          return tagChip(t);
        }).join('') + '</div>'
      : '';
    var httpBadge = httpBadgeHTML(d);
    var head = '<div class="card-head">' +
      '<div class="card-host">' +
        hostLink +
        '<div class="host-meta-row">' +
          '<span class="badge ' + st.cls + '">' + st.label + '</span>' +
          httpBadge +
          (d.tags && d.tags.length ? d.tags.map(function (t) { return tagChip(t); }).join('') : '') +
        '</div>' +
      '</div>' +
      '<div class="card-actions">' +
        '<button class="action-btn' + (checking ? ' checking' : '') + '" data-action="check" data-id="' + d.id + '" title="立即检测"' + (checking ? ' disabled' : '') + '><i class="fas fa-bolt"></i></button>' +
        '<button class="action-btn" data-action="edit" data-id="' + d.id + '" title="编辑"><i class="fas fa-pen"></i></button>' +
        '<button class="action-btn danger" data-action="delete" data-id="' + d.id + '" title="删除"><i class="fas fa-trash"></i></button>' +
      '</div>' +
    '</div>';

    // 错误提示（如果有）
    var errBox = d.last_error
      ? '<div class="card-error"><i class="fas fa-triangle-exclamation"></i> ' + escapeHTML(d.last_error) + '</div>'
      : '';

    // 证书信息块
    var certBlock = '';
    if (d.subject && !d.last_error) {
      var sansExtra = '';
      if (d.sans && d.sans.length > 1) {
        sansExtra = '<button type="button" class="cert-tag san-trigger" data-san-id="' + d.id + '" title="点击查看完整列表">+' +
          (d.sans.length - 1) + ' SAN</button>';
      }
      var caName = d.issuer_org || d.issuer || '—';
      certBlock = '<div class="card-cert"><div class="cert-info">' +
        '<div class="cert-line cert-line-main">' +
          '<span class="cert-cn" title="' + escapeHTML(d.subject) + '">' + escapeHTML(d.subject) + '</span>' +
          (d.is_wildcard ? '<span class="cert-tag">泛域名</span>' : '') +
          sansExtra +
        '</div>' +
        '<div class="cert-line cert-line-meta">' +
          '<span class="cert-label">序列号:</span>' +
          '<span class="cert-val cert-serial" title="' + escapeHTML(d.serial_number) + '">' + escapeHTML(d.serial_number) + '</span>' +
        '</div>' +
        '<div class="cert-line cert-line-meta">' +
          '<span class="cert-label">签发CA:</span>' +
          '<span class="cert-val cert-ca" title="' + escapeHTML(caName) + '">' + escapeHTML(caName) + '</span>' +
        '</div>' +
      '</div></div>';
    }

    // 日期网格：到期 / 检测
    var dates = '<div class="card-dates">' +
      '<div class="card-date-item">' +
        '<div class="card-date-label">到期</div>' +
        (d.not_after && !d.last_error
          ? '<span class="card-date-value">' + fmtDate(d.not_after) + '</span>' + daysBadge(d.days_remaining)
          : '<span class="card-date-sub">—</span>') +
      '</div>' +
      '<div class="card-date-item">' +
        '<div class="card-date-label">检测</div>' +
        (d.last_checked
          ? '<span class="card-date-value">' + fmtRelative(d.last_checked) + '</span>' +
            '<span class="card-date-sub">' + fmtDate(d.last_checked) + '</span>'
          : '<span class="card-date-sub">从未</span>') +
      '</div>' +
    '</div>';

    // 备注
    var notesBlock = d.notes
      ? '<div class="card-notes">' + escapeHTML(d.notes) + '</div>'
      : '';

    return '<div class="card-item" data-id="' + d.id + '">' +
      head + errBox + certBlock + dates + notesBlock +
    '</div>';
  }

  // ===== 事件 =====
  // 搜索（防抖）
  var searchTimer = null;
  $search.addEventListener('input', function () {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(function () {
      state.search = $search.value.trim();
      loadDomains();
    }, 300);
  });

  // 表格 + 卡片双视图共用事件委托
  function onItemClicked(e) {
    var sanBtn = e.target.closest('.san-trigger');
    if (sanBtn) {
      var sid = parseInt(sanBtn.getAttribute('data-san-id'), 10);
      openSanList(sid);
      return;
    }
    var btn = e.target.closest('[data-action]');
    if (!btn) return;
    var id = parseInt(btn.getAttribute('data-id'), 10);
    var action = btn.getAttribute('data-action');
    if (action === 'check') doCheck(id, btn);
    else if (action === 'edit') openEdit(id);
    else if (action === 'delete') openDelete(id);
  }
  $body.addEventListener('click', onItemClicked);
  $cardList.addEventListener('click', onItemClicked);

  // ===== 点击表头按到期时间排序（manual → asc → desc → manual 循环）=====
  var $sortExpiry = document.getElementById('sortExpiry');
  var SORT_CYCLE = ['manual', 'expiry_asc', 'expiry_desc'];

  $sortExpiry.addEventListener('click', function () {
    var cur = SORT_CYCLE.indexOf(state.sortMode);
    state.sortMode = SORT_CYCLE[(cur + 1) % SORT_CYCLE.length];
    updateSortIndicator();
    render();
  });

  function updateSortIndicator() {
    var iconClass = 'fa-sort';
    if (state.sortMode === 'expiry_asc') iconClass = 'fa-sort-up';
    else if (state.sortMode === 'expiry_desc') iconClass = 'fa-sort-down';
    var i = $sortExpiry.querySelector('.sort-indicator i');
    if (i) i.className = 'fas ' + iconClass;
    $sortExpiry.classList.toggle('sort-asc', state.sortMode === 'expiry_asc');
    $sortExpiry.classList.toggle('sort-desc', state.sortMode === 'expiry_desc');
  }

  // ===== 表格行拖拽排序（PC，仅在手柄上按下才允许拖）=====
  // 用 mousedown 检测：手柄按下 → 给该行加 draggable=true；松开/离开 → 复位
  // 这样表格内其他按钮（编辑/删除/检测）正常点击不被影响
  $body.addEventListener('mousedown', function (e) {
    var row = e.target.closest('tr');
    if (!row || !row.dataset.id) return;
    row.draggable = !!e.target.closest('.drag-handle');
  });
  $body.addEventListener('mouseup', function (e) {
    var row = e.target.closest('tr');
    if (row) row.draggable = false;
  });

  var draggedRow = null;

  $body.addEventListener('dragstart', function (e) {
    var row = e.target.closest('tr');
    if (!row || !row.draggable) return;
    draggedRow = row;
    setTimeout(function () { row.classList.add('dragging'); }, 0);
    e.dataTransfer.effectAllowed = 'move';
    try { e.dataTransfer.setData('text/plain', row.dataset.id); } catch (_) {}
  });

  $body.addEventListener('dragover', function (e) {
    if (!draggedRow) return;
    e.preventDefault();
    var targetRow = e.target.closest('tr');
    if (!targetRow || targetRow === draggedRow) return;
    // 清掉其他行的指示线
    var allRows = $body.querySelectorAll('tr');
    for (var i = 0; i < allRows.length; i++) {
      allRows[i].classList.remove('dragover-above', 'dragover-below');
    }
    // 判定鼠标在目标行的上半还是下半
    var box = targetRow.getBoundingClientRect();
    var isAbove = (e.clientY - box.top) < box.height / 2;
    targetRow.classList.add(isAbove ? 'dragover-above' : 'dragover-below');
  });

  $body.addEventListener('drop', function (e) {
    if (!draggedRow) return;
    e.preventDefault();
    var targetRow = e.target.closest('tr');
    if (targetRow && targetRow !== draggedRow) {
      var box = targetRow.getBoundingClientRect();
      var isAbove = (e.clientY - box.top) < box.height / 2;
      if (isAbove) {
        $body.insertBefore(draggedRow, targetRow);
      } else {
        $body.insertBefore(draggedRow, targetRow.nextSibling);
      }
      saveDomainOrder();
    }
    cleanupDrag();
  });

  $body.addEventListener('dragend', cleanupDrag);

  function cleanupDrag() {
    if (draggedRow) draggedRow.classList.remove('dragging');
    draggedRow = null;
    var allRows = $body.querySelectorAll('tr');
    for (var i = 0; i < allRows.length; i++) {
      allRows[i].classList.remove('dragover-above', 'dragover-below');
    }
  }

  function saveDomainOrder() {
    var rows = $body.querySelectorAll('tr[data-id]');
    var ids = [];
    for (var i = 0; i < rows.length; i++) {
      ids.push(parseInt(rows[i].getAttribute('data-id'), 10));
    }
    api('PUT', '/api/domains/reorder', { domain_ids: ids }).then(function () {
      toast('排序已保存', 'success');
      // 更新 state 顺序，避免下次 render 用旧顺序
      state.domains.sort(function (a, b) {
        return ids.indexOf(a.id) - ids.indexOf(b.id);
      });
    }).catch(function (err) {
      toast('排序失败：' + err.message, 'error');
      loadDomains();  // 失败时重拉，恢复服务端顺序
    });
  }

  // SAN 列表弹窗
  var $sanListTitle = document.getElementById('sanListTitle');
  var $sanListSub = document.getElementById('sanListSub');
  var $sanListBody = document.getElementById('sanListBody');

  function openSanList(id) {
    var d = state.domains.find(function (x) { return x.id === id; });
    if (!d || !d.sans || !d.sans.length) return;
    $sanListTitle.textContent = '证书覆盖的域名';
    $sanListSub.textContent = d.host + ' · 共 ' + d.sans.length + ' 个域名';
    var html = d.sans.map(function (s) {
      var isWild = s.indexOf('*.') === 0;
      var isPrimary = s === d.subject;
      var cls = 'san-item' + (isWild ? ' san-wild' : '') + (isPrimary ? ' san-primary' : '');
      var tag = isWild ? '<span class="san-tag">泛</span>'
             : isPrimary ? '<span class="san-tag">主</span>'
             : '<span class="san-tag">单</span>';
      return '<div class="' + cls + '">' + tag + '<span class="san-name">' + escapeHTML(s) + '</span></div>';
    }).join('');
    $sanListBody.innerHTML = html;
    Modal.open('sanListModal');
  }

  // 新增按钮
  document.getElementById('addBtn').addEventListener('click', openAdd);

  function openAdd() {
    $formTitle.textContent = '新增网址';
    $formSub.textContent = '添加后可立即检测证书状态';
    $formId.value = '';
    $formHost.value = '';
    $formNotes.value = '';
    selectedTagIDs = {};
    renderTagPicker();
    updateNotesCount();
    Modal.open('domainModal');
    setTimeout(function () { $formHost.focus(); }, 100);
  }

  // 把 host + port + path 重组成 URL 字符串，用于编辑时回填输入框
  function buildURL(host, port, path) {
    var s = host || '';
    if (port && port !== 443) s += ':' + port;
    if (path && path !== '/' && path !== '') s += path;
    return s;
  }

  function openEdit(id) {
    var d = state.domains.find(function (x) { return x.id === id; });
    if (!d) return;
    $formTitle.textContent = '编辑网址';
    $formSub.textContent = '修改 ' + d.host + ' 的信息';
    $formId.value = d.id;
    $formHost.value = buildURL(d.host, d.port, d.path);
    $formNotes.value = d.notes || '';
    selectedTagIDs = {};
    (d.tags || []).forEach(function (t) { selectedTagIDs[t.id] = true; });
    renderTagPicker();
    updateNotesCount();
    Modal.open('domainModal');
    setTimeout(function () { $formHost.focus(); }, 100);
  }

  // 渲染标签选择器（基于 allTags 和 selectedTagIDs）
  function renderTagPicker() {
    if (!allTags.length) {
      $tagPicker.innerHTML = '<span class="tag-picker-empty">暂无标签，请先到右上角「设置」添加</span>';
      return;
    }
    $tagPicker.innerHTML = allTags.map(function (t) {
      var isActive = !!selectedTagIDs[t.id];
      var iconHTML = t.icon ? '<i class="fas ' + escapeHTML(t.icon) + '"></i>' : '';
      var style = '';
      if (t.color) {
        // 有颜色：选中态用更深背景 + 白字，未选用浅背景 + 彩字
        var bg = hexToRgba(t.color, isActive ? 0.45 : 0.15);
        var textColor = isActive ? '#fff' : t.color;
        style = ' style="background:' + bg + ';border-color:' + t.color + ';color:' + textColor + ';"';
      }
      return '<button type="button" class="tag-chip' + (isActive ? ' active' : '') + '" data-tag-id="' + t.id + '"' + style + '>' +
        iconHTML + escapeHTML(t.name) + '</button>';
    }).join('');
  }

  // 点击 chip 切换选中
  $tagPicker.addEventListener('click', function (e) {
    var chip = e.target.closest('.tag-chip');
    if (!chip) return;
    var id = parseInt(chip.getAttribute('data-tag-id'), 10);
    if (selectedTagIDs[id]) {
      delete selectedTagIDs[id];
      chip.classList.remove('active');
    } else {
      selectedTagIDs[id] = true;
      chip.classList.add('active');
    }
  });

  // 拉取标签列表（启动时和打开设置弹窗后都调一次）
  function refreshAllTags() {
    return api('GET', '/api/tags').then(function (list) {
      allTags = list || [];
      renderTagFilter();
    }).catch(function () {});
  }

  // ===== 标签筛选（工具栏） =====
  var $tagFilter = document.getElementById('tagFilter');

  function renderTagFilter() {
    // 永远显示筛选行（至少有"10天内到期"快捷过滤）
    $tagFilter.hidden = false;
    var html = '<span class="tag-filter-label"><i class="fas fa-filter"></i></span>';
    // 10 天内到期快捷过滤（永远显示，永远可点）
    var expActive = state.filterExpiringSoon;
    html += '<button type="button" class="filter-chip filter-expiring' + (expActive ? ' active' : '') + '" data-filter-expiring="1" title="筛选 10 天内到期的域名">' +
      '<i class="fas fa-triangle-exclamation"></i> 10天内到期' +
    '</button>';
    html += allTags.map(function (t) {
      var isActive = !!state.filterTagIDs[t.id];
      var iconHTML = t.icon ? '<i class="fas ' + escapeHTML(t.icon) + '"></i>' : '';
      var style = '';
      if (t.color) {
        var bg = hexToRgba(t.color, isActive ? 0.4 : 0.15);
        var textColor = isActive ? '#fff' : t.color;
        style = ' style="background:' + bg + ';border-color:' + t.color + ';color:' + textColor + ';"';
      }
      return '<button type="button" class="filter-chip' + (isActive ? ' active' : '') + '" data-filter-tag-id="' + t.id + '"' + style + '>' +
        iconHTML + escapeHTML(t.name) + '</button>';
    }).join('');
    $tagFilter.innerHTML = html;
  }

  $tagFilter.addEventListener('click', function (e) {
    // 10 天内到期快捷过滤（客户端筛选，直接 render）
    var expiringBtn = e.target.closest('[data-filter-expiring]');
    if (expiringBtn) {
      state.filterExpiringSoon = !state.filterExpiringSoon;
      render();
      return;
    }
    var chip = e.target.closest('.filter-chip');
    if (!chip) return;
    var id = parseInt(chip.getAttribute('data-filter-tag-id'), 10);
    if (state.filterTagIDs[id]) {
      delete state.filterTagIDs[id];
      chip.classList.remove('active');
    } else {
      state.filterTagIDs[id] = true;
      chip.classList.add('active');
    }
    loadDomains();
  });

  function openDelete(id) {
    var d = state.domains.find(function (x) { return x.id === id; });
    if (!d) return;
    pendingDeleteId = id;
    $deleteHost.textContent = d.host;
    Modal.open('deleteModal');
  }

  // 表单提交（新增 / 编辑）— 直接发 URL，后端解析
  $form.addEventListener('submit', function (e) {
    e.preventDefault();
    var url = ($formHost.value || '').trim();
    if (!url) {
      toast('请输入网址', 'error');
      $formHost.focus();
      return;
    }
    var notes = $formNotes.value.trim();
    var tagIDs = Object.keys(selectedTagIDs).map(function (s) { return parseInt(s, 10); });
    var body = { url: url, notes: notes, tag_ids: tagIDs };

    var id = $formId.value;
    var req = id
      ? api('PUT', '/api/domains/' + id, body)
      : api('POST', '/api/domains', body);

    $formSubmit.disabled = true;
    req.then(function () {
      Modal.close('domainModal');
      toast(id ? '已更新' : '已添加', 'success');
      loadDomains();
    }).catch(function (err) {
      toast('保存失败：' + err.message, 'error');
    }).finally(function () {
      $formSubmit.disabled = false;
    });
  });

  // 删除确认
  $deleteConfirm.addEventListener('click', function () {
    if (!pendingDeleteId) return;
    var id = pendingDeleteId;
    $deleteConfirm.disabled = true;
    api('DELETE', '/api/domains/' + id).then(function () {
      Modal.close('deleteModal');
      toast('已删除', 'success');
      loadDomains();
    }).catch(function (err) {
      toast('删除失败：' + err.message, 'error');
    }).finally(function () {
      pendingDeleteId = null;
      $deleteConfirm.disabled = false;
    });
  });

  // 立即检测
  function doCheck(id, btn) {
    state.checkingIds[id] = true;
    btn.classList.add('checking');
    btn.disabled = true;
    api('POST', '/api/domains/' + id + '/check').then(function (updated) {
      // 局部替换
      var idx = state.domains.findIndex(function (x) { return x.id === id; });
      if (idx >= 0) state.domains[idx] = updated;
      toast(updated.last_error ? '检测失败：' + updated.last_error : '检测完成 · 剩 ' + updated.days_remaining + ' 天',
        updated.last_error ? 'error' : 'success');
    }).catch(function (err) {
      toast('检测失败：' + err.message, 'error');
    }).finally(function () {
      delete state.checkingIds[id];
      render();
    });
  }

  // ===== 标签管理 =====
  var $tagInput = document.getElementById('tagInput');
  var $tagAddBtn = document.getElementById('tagAddBtn');
  var $tagList = document.getElementById('tagList');

  var currentTagsCache = [];  // 给图标/颜色浮层查找当前选中态

  function loadTags() {
    return api('GET', '/api/tags').then(function (tags) {
      currentTagsCache = tags || [];
      renderTags(currentTagsCache);
    }).catch(function (err) {
      $tagList.innerHTML = '<div class="tag-empty">加载失败：' + escapeHTML(err.message) + '</div>';
    });
  }

  // ===== 图标 / 颜色调色板 =====
  var ICONS = [
    'fa-globe', 'fa-server', 'fa-database', 'fa-shield-halved', 'fa-lock', 'fa-star',
    'fa-heart', 'fa-bolt', 'fa-code', 'fa-flask', 'fa-rocket', 'fa-wrench',
    'fa-cloud', 'fa-house', 'fa-building', 'fa-tag', 'fa-gear', 'fa-eye',
    'fa-triangle-exclamation', 'fa-circle', 'fa-cube', 'fa-network-wired',
    'fa-leaf', 'fa-fire'
  ];
  var COLORS = [
    '#409eff', '#22C55E', '#FFD166', '#FF6B6B', '#A78BFA', '#06B6D4',
    '#EC4899', '#94A3B8', '#FB923C', '#6366F1', '#14B8A6', '#F43F5E'
  ];

  function renderTags(tags) {
    if (!tags.length) {
      $tagList.innerHTML = '<div class="tag-empty">暂无标签，添加第一个吧</div>';
      return;
    }
    $tagList.innerHTML = tags.map(function (t) {
      var iconHTML = t.icon
        ? '<i class="fas ' + escapeHTML(t.icon) + '"></i>'
        : '<i class="fas fa-tag" style="opacity:.3"></i>';
      var colorStyle = t.color
        ? 'background:' + escapeHTML(t.color) + ';'
        : '';
      var colorClass = 'tag-color-btn' + (t.color ? '' : ' unset');
      return '<div class="tag-item" draggable="true" data-tag-id="' + t.id + '">' +
        '<span class="tag-handle" title="拖动排序"><i class="fas fa-grip-vertical"></i></span>' +
        '<button type="button" class="tag-icon-btn" data-icon-pick="' + t.id + '" title="设置图标">' + iconHTML + '</button>' +
        '<button type="button" class="' + colorClass + '" data-color-pick="' + t.id + '" style="' + colorStyle + '" title="设置颜色"></button>' +
        '<span class="tag-name">' + escapeHTML(t.name) + '</span>' +
        '<button type="button" class="tag-delete" data-tag-id="' + t.id + '" title="删除"><i class="fas fa-times"></i></button>' +
      '</div>';
    }).join('');
  }

  // ===== 图标 / 颜色 浮层（事件委托，浮层挂到 body 避免 overflow 裁切）=====
  document.addEventListener('click', function (e) {
    // 1. 点开图标选择器
    var iconBtn = e.target.closest('[data-icon-pick]');
    if (iconBtn) {
      e.stopPropagation();
      closeTagPickers();
      openIconPicker(iconBtn);
      return;
    }
    // 2. 点开颜色选择器
    var colorBtn = e.target.closest('[data-color-pick]');
    if (colorBtn) {
      e.stopPropagation();
      closeTagPickers();
      openColorPicker(colorBtn);
      return;
    }
    // 3. 浮层内：选图标
    var iconOpt = e.target.closest('[data-set-icon]');
    if (iconOpt) {
      updateTagField(parseInt(iconOpt.getAttribute('data-tag-id'), 10), 'icon', iconOpt.getAttribute('data-set-icon'));
      closeTagPickers();
      return;
    }
    // 4. 浮层内：选颜色
    var colorOpt = e.target.closest('[data-set-color]');
    if (colorOpt) {
      updateTagField(parseInt(colorOpt.getAttribute('data-tag-id'), 10), 'color', colorOpt.getAttribute('data-set-color'));
      closeTagPickers();
      return;
    }
    // 5. 浮层内：清除图标
    var iconClear = e.target.closest('[data-clear-icon]');
    if (iconClear) {
      updateTagField(parseInt(iconClear.getAttribute('data-tag-id'), 10), 'icon', '');
      closeTagPickers();
      return;
    }
    // 6. 浮层内：清除颜色
    var colorClear = e.target.closest('[data-clear-color]');
    if (colorClear) {
      updateTagField(parseInt(colorClear.getAttribute('data-tag-id'), 10), 'color', '');
      closeTagPickers();
      return;
    }
    // 7. 点浮层外 → 关闭
    if (!e.target.closest('.tag-picker-pop')) {
      closeTagPickers();
    }
  });

  // ESC 也能关
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') closeTagPickers();
  });
  // 滚动 / 窗口尺寸变化时关闭（坐标会失效）
  window.addEventListener('scroll', closeTagPickers, true);
  window.addEventListener('resize', closeTagPickers);

  function closeTagPickers() {
    var pops = document.querySelectorAll('body > .tag-picker-pop');
    for (var i = 0; i < pops.length; i++) pops[i].remove();
  }

  function openIconPicker(anchor) {
    var tagID = parseInt(anchor.getAttribute('data-icon-pick'), 10);
    var t = (currentTagsCache.find(function (x) { return x.id === tagID; }) || {});
    var html = '<div class="tag-picker-pop-title">选择图标</div>' +
      '<div class="icon-grid">' + ICONS.map(function (ic) {
        var active = t.icon === ic ? ' active' : '';
        return '<button type="button" class="icon-option' + active + '" data-set-icon="' + ic + '" data-tag-id="' + tagID + '" title="' + ic + '">' +
          '<i class="fas ' + ic + '"></i></button>';
      }).join('') + '</div>' +
      '<button type="button" class="icon-clear" data-clear-icon="1" data-tag-id="' + tagID + '">清除图标</button>';
    showTagPicker(anchor, html);
  }

  function openColorPicker(anchor) {
    var tagID = parseInt(anchor.getAttribute('data-color-pick'), 10);
    var t = (currentTagsCache.find(function (x) { return x.id === tagID; }) || {});
    var html = '<div class="tag-picker-pop-title">选择颜色</div>' +
      '<div class="color-grid">' + COLORS.map(function (co) {
        var active = t.color === co ? ' active' : '';
        return '<button type="button" class="color-option' + active + '" data-set-color="' + co + '" data-tag-id="' + tagID + '" style="background:' + co + '" title="' + co + '"></button>';
      }).join('') + '</div>' +
      '<button type="button" class="color-clear" data-clear-color="1" data-tag-id="' + tagID + '">清除颜色（用默认蓝）</button>';
    showTagPicker(anchor, html);
  }

  // 浮层挂到 body，用 fixed 定位 + JS 算坐标，避免被父级 overflow 裁切
  function showTagPicker(anchor, innerHTML) {
    var pop = document.createElement('div');
    pop.className = 'tag-picker-pop';
    pop.innerHTML = innerHTML;
    document.body.appendChild(pop);

    var rect = anchor.getBoundingClientRect();
    var popRect = pop.getBoundingClientRect();
    var margin = 6;

    // 横向：默认左对齐 anchor，超出右边则右对齐
    var left = rect.left;
    if (left + popRect.width > window.innerWidth - 10) {
      left = window.innerWidth - popRect.width - 10;
    }
    if (left < 10) left = 10;

    // 纵向：优先往下；下面不够且上面够则往上
    var top = rect.bottom + margin;
    if (top + popRect.height > window.innerHeight - 10 && rect.top - popRect.height - margin > 10) {
      top = rect.top - popRect.height - margin;
    }

    pop.style.left = left + 'px';
    pop.style.top = top + 'px';
  }

  // 增量更新单个字段：PUT /api/tags/:id {icon|color|name: ...}
  function updateTagField(tagID, field, value) {
    var body = {};
    body[field] = value;
    api('PUT', '/api/tags/' + tagID, body).then(function () {
      toast('已更新', 'success');
      loadTags();           // 刷新设置弹窗里的列表
      refreshAllTags();     // 同步给域名表单/筛选条
    }).catch(function (err) {
      toast('更新失败：' + err.message, 'error');
    });
  }

  // ===== 拖拽排序（PC 端）=====
  var draggedItem = null;

  $tagList.addEventListener('dragstart', function (e) {
    var item = e.target.closest('.tag-item');
    if (!item) return;
    draggedItem = item;
    setTimeout(function () { item.classList.add('dragging'); }, 0);
    e.dataTransfer.effectAllowed = 'move';
    // Firefox 需要 setData 才能触发 drag
    try { e.dataTransfer.setData('text/plain', item.dataset.tagId); } catch (_) {}
  });

  $tagList.addEventListener('dragover', function (e) {
    if (!draggedItem) return;
    e.preventDefault();
    var after = getDragAfter($tagList, e.clientY);
    if (after == null) {
      $tagList.appendChild(draggedItem);
    } else if (after !== draggedItem) {
      $tagList.insertBefore(draggedItem, after);
    }
  });

  $tagList.addEventListener('dragend', function () {
    if (!draggedItem) return;
    draggedItem.classList.remove('dragging');
    draggedItem = null;
    saveTagOrder();
  });

  // 找到鼠标 Y 位置应该插到哪个元素之前
  function getDragAfter(container, y) {
    var elems = container.querySelectorAll('.tag-item:not(.dragging)');
    var closest = { offset: -Infinity, element: null };
    elems.forEach(function (el) {
      var box = el.getBoundingClientRect();
      var offset = y - box.top - box.height / 2;
      if (offset < 0 && offset > closest.offset) {
        closest = { offset: offset, element: el };
      }
    });
    return closest.element;
  }

  function saveTagOrder() {
    var ids = Array.prototype.slice.call($tagList.querySelectorAll('.tag-item'))
      .map(function (el) { return parseInt(el.getAttribute('data-tag-id'), 10); });
    api('PUT', '/api/tags/reorder', { tag_ids: ids }).then(function () {
      toast('排序已保存', 'success');
      refreshAllTags();  // 同步给域名表单/筛选条
    }).catch(function (err) {
      toast('排序失败：' + err.message, 'error');
      loadTags();  // 失败时重拉，恢复服务端顺序
    });
  }

  function addTag() {
    var name = $tagInput.value.trim();
    if (!name) {
      toast('请输入标签名', 'error');
      $tagInput.focus();
      return;
    }
    api('POST', '/api/tags', { name: name }).then(function () {
      $tagInput.value = '';
      toast('已添加', 'success');
      loadTags();
      refreshAllTags();  // 同步给域名表单的标签选择器
    }).catch(function (err) {
      toast('添加失败：' + err.message, 'error');
    });
  }

  $tagAddBtn.addEventListener('click', addTag);
  $tagInput.addEventListener('keydown', function (e) {
    if (e.key === 'Enter') { e.preventDefault(); addTag(); }
  });

  // 删除标签（事件委托）
  $tagList.addEventListener('click', function (e) {
    var btn = e.target.closest('.tag-delete');
    if (!btn) return;
    var id = parseInt(btn.getAttribute('data-tag-id'), 10);
    api('DELETE', '/api/tags/' + id).then(function () {
      toast('已删除', 'success');
      loadTags();
      refreshAllTags();  // 同步给域名表单的标签选择器
    }).catch(function (err) {
      toast('删除失败：' + err.message, 'error');
    });
  });

  // 打开设置弹窗时加载标签
  document.addEventListener('click', function (e) {
    var opener = e.target.closest('[data-modal-open="settingsModal"]');
    if (opener) {
      setTimeout(loadTags, 50);
    }
  });

  // ===== 启动 =====
  refreshAllTags().then(loadDomains);

  // 定时刷新（每 60s 拉取最新数据，不打断用户操作）
  setInterval(function () {
    if (!document.hidden) loadDomains();
  }, 60000);
})();