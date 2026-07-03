(function () {
  // ===== 通用弹窗 =====
  // 触发：任何元素加 data-modal-open="modalId" → 打开 #modalId
  // 关闭：data-modal-close 元素、点击遮罩本身、按 ESC
  function open(modal) {
    modal.classList.add('show');
    modal.setAttribute('aria-hidden', 'false');
  }
  function close(modal) {
    modal.classList.remove('show');
    modal.setAttribute('aria-hidden', 'true');
  }

  function init() {
    var modals = document.querySelectorAll('.modal-overlay');
    if (!modals.length) return;

    // 打开：完整 click 触发（与按钮一致）
    document.addEventListener('click', function (e) {
      var opener = e.target.closest('[data-modal-open]');
      if (opener) {
        var id = opener.getAttribute('data-modal-open');
        var modal = document.getElementById(id);
        if (modal) {
          if (opener.tagName === 'A') e.preventDefault();
          open(modal);
        }
      }
    });

    // 关闭：用 mousedown 而不是 click，避免在表单内按下、移动到遮罩松开
    // 时被浏览器算作"点击遮罩"而误关（click 按规范触发在 mousedown/mouseup
    // 共同祖先上）
    document.addEventListener('mousedown', function (e) {
      var closer = e.target.closest('[data-modal-close]');
      if (closer) {
        close(closer.closest('.modal-overlay'));
        return;
      }
      // 仅当按下点就是遮罩本身（不是遮罩内的卡片）才关闭
      if (e.target.classList.contains('modal-overlay')) {
        close(e.target);
      }
    }, true);

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