(function () {
  // ===== 通用弹窗 =====
  // 触发：任何元素加 data-modal-open="modalId" → 打开 #modalId
  // 关闭：data-modal-close 元素、点击遮罩本身、按 ESC
  function open(modal) { modal.classList.add('show'); }
  function close(modal) { modal.classList.remove('show'); }

  function init() {
    var modals = document.querySelectorAll('.modal-overlay');
    if (!modals.length) return;

    document.addEventListener('click', function (e) {
      var opener = e.target.closest('[data-modal-open]');
      if (opener) {
        var id = opener.getAttribute('data-modal-open');
        var modal = document.getElementById(id);
        if (modal) {
          if (opener.tagName === 'A') e.preventDefault();
          open(modal);
        }
        return;
      }
      var closer = e.target.closest('[data-modal-close]');
      if (closer) {
        close(closer.closest('.modal-overlay'));
        return;
      }
      if (e.target.classList.contains('modal-overlay')) {
        close(e.target);
      }
    });

    document.addEventListener('keydown', function (e) {
      if (e.key !== 'Escape') return;
      for (var i = modals.length - 1; i >= 0; i--) {
        if (modals[i].classList.contains('show')) {
          close(modals[i]);
          break;
        }
      }
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

  // 暴露 API 供外部调用：window.Modal.open('id') / Modal.close('id')
  window.Modal = {
    open: function (id) { var m = document.getElementById(id); if (m) open(m); },
    close: function (id) { var m = document.getElementById(id); if (m) close(m); }
  };
})();