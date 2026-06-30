(function () {
  var form = document.getElementById('loginForm');
  var tip = document.getElementById('loginTip');
  var btn = form.querySelector('.login-btn');
  var btnText = btn.querySelector('span');
  var captchaInput = document.getElementById('captcha');
  var captchaImg = document.getElementById('captchaImg');

  var captchaId = '';

  function refreshCaptcha() {
    fetch('/api/captcha', { credentials: 'same-origin' })
      .then(function (r) { return r.json(); })
      .then(function (res) {
        if (res.code === 0) {
          captchaId = res.data.id;
          captchaImg.src = res.data.img;
        }
      })
      .catch(function () {});
  }

  function setBtnState(text) {
    if (text) {
      btnText.textContent = text;
      btn.setAttribute('disabled', 'disabled');
    } else {
      btnText.textContent = '进入证书监控后台';
      btn.removeAttribute('disabled');
    }
  }

  captchaImg.addEventListener('click', function () {
    refreshCaptcha();
    captchaInput.focus();
  });

  form.addEventListener('submit', function (e) {
    e.preventDefault();
    tip.hidden = true;
    setBtnState('登录中...');

    var body = new URLSearchParams(new FormData(form));
    body.append('captchaId', captchaId);

    fetch('/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body,
      credentials: 'same-origin'
    })
      .then(function (r) { return r.json(); })
      .then(function (res) {
        if (res.code === 0) {
          location.href = '/domains';
        } else {
          showError(res.msg || '登录失败');
        }
      })
      .catch(function () { showError('网络异常，请重试'); })
      .finally(function () { setBtnState(''); });
  });

  function showError(msg) {
    tip.textContent = msg;
    tip.hidden = false;
    captchaInput.value = '';
    refreshCaptcha();
  }

  refreshCaptcha();

  // 鼠标视差：粒子轻微跟随鼠标
  var particles = document.querySelectorAll('.bg-particle');
  document.addEventListener('mousemove', function (e) {
    var x = e.clientX / window.innerWidth;
    var y = e.clientY / window.innerHeight;
    for (var i = 0; i < particles.length; i++) {
      particles[i].style.transform = 'translate(' + (x * 30) + 'px, ' + (y * 30) + 'px)';
    }
  });
})();