(function () {
  'use strict';

  // ===== 状态 =====
  var state = {
    domains: [],
    search: '',
    page: 1,
    pageSize: 20,
    checkingIds: {} // 正在检测中的域名 id
  };

  // ===== DOM 引用 =====
  var $body = document.getElementById('domainsBody');
  var $cardList = document.getElementById('cardList');
  var $empty = document.getElementById('emptyState');
  var $count = document.getElementById('countBadge');
  var $search = document.getElementById('searchInput');
  var $pagination = document.getElementById('pagination');
  var $pageInfo = document.getElementById('pageInfo');
  var $prev = document.getElementById('prevPage');
  var $next = document.getElementById('nextPage');
  var $pageSize = document.getElementById('pageSizeSelect');
  var $toast = document.getElementById('toast');

  // 表单弹窗
  var $form = document.getElementById('domainForm');
  var $formTitle = document.getElementById('domainModalTitle');
  var $formSub = document.getElementById('domainModalSub');
  var $formId = document.getElementById('domainId');
  var $formHost = document.getElementById('hostInput');
  var $formNotes = document.getElementById('notesInput');
  var $formSubmit = document.getElementById('domainSubmitBtn');
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

  // 计算状态：返回 {cls, label}
  function statusOf(d) {
    if (d.last_error) return { cls: 'badge-err', label: '连接失败' };
    if (!d.not_after) return { cls: 'badge-err', label: '未检测' };
    if (d.days_remaining < 0) return { cls: 'badge-danger', label: '已过期' };
    if (d.days_remaining <= 7) return { cls: 'badge-danger', label: '紧急' };
    if (d.days_remaining <= 30) return { cls: 'badge-warn', label: '将过期' };
    return { cls: 'badge-ok', label: '健康' };
  }

  function daysBadge(days) {
    var cls = 'days-ok', text;
    if (days < 0) { cls = 'days-danger'; text = '已过期 ' + Math.abs(days) + ' 天'; }
    else if (days <= 7) { cls = 'days-danger'; text = '剩 ' + days + ' 天'; }
    else if (days <= 30) { cls = 'days-warn'; text = '剩 ' + days + ' 天'; }
    else { text = '剩 ' + days + ' 天'; }
    return '<span class="expiry-days ' + cls + '">' + text + '</span>';
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
    $body.innerHTML = '<tr class="loading-row"><td colspan="6"><div class="spinner"></div></td></tr>';
    $empty.hidden = true;
    return api('GET', url).then(function (list) {
      state.domains = list || [];
      render();
    }).catch(function (err) {
      $body.innerHTML = '<tr><td colspan="6" style="padding:30px;text-align:center;color:#ff6b6b;">加载失败：' +
        escapeHTML(err.message) + '</td></tr>';
    });
  }

  // ===== 渲染 =====
  function filteredSorted() {
    var list = state.domains.slice();
    list.sort(function (a, b) {
      // 异常 / 未检测 排最前；其次按剩余天数升序
      var ax = a.last_error ? -1e9 : (a.days_remaining === 0 && !a.not_after ? -1e9 : a.days_remaining);
      var bx = b.last_error ? -1e9 : (b.days_remaining === 0 && !b.not_after ? -1e9 : b.days_remaining);
      return ax - bx;
    });
    return list;
  }

  function render() {
    var list = filteredSorted();
    $count.textContent = list.length + ' 条';

    if (!list.length) {
      $body.innerHTML = '';
      $cardList.innerHTML = '';
      $empty.hidden = false;
      $pagination.hidden = true;
      return;
    }
    $empty.hidden = true;

    var totalPages = Math.max(1, Math.ceil(list.length / state.pageSize));
    if (state.page > totalPages) state.page = totalPages;
    var start = (state.page - 1) * state.pageSize;
    var slice = list.slice(start, start + state.pageSize);

    // 双视图同步渲染（CSS 决定显示哪个）
    $body.innerHTML = slice.map(renderRow).join('');
    $cardList.innerHTML = slice.map(renderCard).join('');

    // 分页
    $pagination.hidden = totalPages <= 1;
    $pageInfo.textContent = state.page + ' / ' + totalPages;
    $prev.disabled = state.page <= 1;
    $next.disabled = state.page >= totalPages;
  }

  function renderRow(d) {
    var st = statusOf(d);
    var checking = state.checkingIds[d.id];

    // 域名 + 状态徽章（host 第一行，状态第二行，端口可选第三行）
    var portSuffix = d.port && d.port !== 443 ? ':' + d.port : '';
    var host = '<td class="col-host"><div class="host-cell">' +
      '<span class="host-name">' + escapeHTML(d.host) + '</span>' +
      '<span class="badge ' + st.cls + '">' + st.label + '</span>' +
      (portSuffix ? '<span class="host-meta">' + escapeHTML(portSuffix) + '</span>' : '') +
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

    return '<tr data-id="' + d.id + '">' +
      host + cert + expiry + checked + notes + actions +
      '</tr>';
  }

  // 渲染移动端卡片视图（与 renderRow 共用 helpers，事件用同一 handler）
  function renderCard(d) {
    var st = statusOf(d);
    var checking = state.checkingIds[d.id];

    // 头部：host + 状态 + 操作
    var head = '<div class="card-head">' +
      '<div class="card-host">' +
        '<span class="host-name">' + escapeHTML(d.host) + '</span>' +
        '<span class="badge ' + st.cls + '">' + st.label + '</span>' +
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
      state.page = 1;
      loadDomains();
    }, 300);
  });

  // 分页
  $prev.addEventListener('click', function () { if (state.page > 1) { state.page--; render(); } });
  $next.addEventListener('click', function () {
    var total = Math.ceil(state.domains.length / state.pageSize);
    if (state.page < total) { state.page++; render(); }
  });
  $pageSize.addEventListener('change', function () {
    state.pageSize = parseInt($pageSize.value, 10) || 20;
    state.page = 1;
    render();
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
    $formTitle.textContent = '新增域名';
    $formSub.textContent = '添加后可立即检测证书状态';
    $formId.value = '';
    $formHost.value = '';
    $formNotes.value = '';
    updateNotesCount();
    Modal.open('domainModal');
    setTimeout(function () { $formHost.focus(); }, 100);
  }

  function openEdit(id) {
    var d = state.domains.find(function (x) { return x.id === id; });
    if (!d) return;
    $formTitle.textContent = '编辑域名';
    $formSub.textContent = '修改 ' + d.host + ' 的信息';
    $formId.value = d.id;
    $formHost.value = d.host;
    $formNotes.value = d.notes || '';
    updateNotesCount();
    Modal.open('domainModal');
    setTimeout(function () { $formHost.focus(); }, 100);
  }

  function openDelete(id) {
    var d = state.domains.find(function (x) { return x.id === id; });
    if (!d) return;
    pendingDeleteId = id;
    $deleteHost.textContent = d.host;
    Modal.open('deleteModal');
  }

  // 表单提交（新增 / 编辑）
  $form.addEventListener('submit', function (e) {
    e.preventDefault();
    var host = normalizeHost($formHost.value);
    if (!host) {
      toast('请输入域名', 'error');
      $formHost.focus();
      return;
    }
    var notes = $formNotes.value.trim();
    var body = { host: host, notes: notes };

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

  // ===== 启动 =====
  loadDomains();

  // 定时刷新（每 60s 拉取最新数据，不打断用户操作）
  setInterval(function () {
    if (!document.hidden) loadDomains();
  }, 60000);
})();