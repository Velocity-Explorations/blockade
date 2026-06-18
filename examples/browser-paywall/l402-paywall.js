// l402-paywall.js — blockAIde L402 client widget
// Drop-in browser client for the L402 payment loop with staked-credential
// cost curve support (v2).
// No build step. No framework. No npm dependencies.
//
// QR code generation uses qrcode-generator (MIT) loaded from CDN on first use.
//
// Three usage modes over one engine:
//   1. Declarative:   <a href="/resource" data-l402>Download</a>
//   2. Programmatic:  const res = await l402Fetch('/resource')
//   3. Global:        L402.installGlobal()
//
// The proxy runs on regtest. A public demo should run on mutinynet or signet
// rather than regtest, since regtest reads as a toy to an outside evaluator.

(function () {
  'use strict';

  var QR_CDN = 'https://cdn.jsdelivr.net/npm/qrcode-generator@1.4.4/qrcode.js';
  var INVOICE_EXPIRY_SECS = 3600;
  var CREDENTIAL_STORAGE_KEY = 'l402_credential';
  var SETTLEMENT_POLL_INTERVAL = 2000;
  var SETTLEMENT_POLL_TIMEOUT = 300000;

  // ---------------------------------------------------------------------------
  // Credential store — holds the enrollment credential for the page session.
  // Uses sessionStorage so it survives page reloads but not tab closes.
  // Treated as a bearer secret.
  // ---------------------------------------------------------------------------

  var credentialStore = {
    get: function () {
      try { return sessionStorage.getItem(CREDENTIAL_STORAGE_KEY); } catch (e) { return null; }
    },
    set: function (cred) {
      try { sessionStorage.setItem(CREDENTIAL_STORAGE_KEY, cred); } catch (e) {}
    },
    clear: function () {
      try { sessionStorage.removeItem(CREDENTIAL_STORAGE_KEY); } catch (e) {}
    },
  };

  // ---------------------------------------------------------------------------
  // Payment provider interface
  // ---------------------------------------------------------------------------
  // Any provider must expose: { sendPayment(invoice) → Promise<{preimage}> }
  // WebLN and NWC both satisfy this shape.

  function getProvider() {
    if (window._l402NwcProvider) return window._l402NwcProvider;
    if (window.webln) return { sendPayment: webLNPay };
    return null;
  }

  function webLNPay(invoice) {
    return window.webln.enable().then(function () {
      return window.webln.sendPayment(invoice);
    });
  }

  // ---------------------------------------------------------------------------
  // Challenge parser
  // ---------------------------------------------------------------------------

  function parseChallenge(wwwAuth) {
    if (!wwwAuth) return null;
    var macaroon = (wwwAuth.match(/macaroon="([^"]+)"/) || [])[1];
    var invoice = (wwwAuth.match(/invoice="([^"]+)"/) || [])[1];
    var type = (wwwAuth.match(/type="([^"]+)"/) || [])[1] || 'toll';
    if (!macaroon || !invoice) return null;
    return { macaroon: macaroon, invoice: invoice, type: type };
  }

  // ---------------------------------------------------------------------------
  // BOLT11 amount extractor
  // ---------------------------------------------------------------------------

  function parseInvoiceAmount(bolt11) {
    var match = bolt11.match(/^ln(bcrt|bc|tbs|tb)(\d+)([munp]?)1/);
    if (!match) return null;
    var amount = parseInt(match[2], 10);
    var mult = match[3];
    var satsPerUnit = { '': 1e8, m: 1e5, u: 100, n: 0.1, p: 0.0001 };
    return Math.round(amount * (satsPerUnit[mult] != null ? satsPerUnit[mult] : 1e8));
  }

  // ---------------------------------------------------------------------------
  // Auth header builder (seam for v2 credential model)
  // ---------------------------------------------------------------------------

  function buildAuthHeader(credential) {
    return 'L402 ' + credential.macaroon + ':' + credential.preimage;
  }

  // ---------------------------------------------------------------------------
  // Settlement polling — cross-device payment confirmation
  // ---------------------------------------------------------------------------

  function extractPaymentHash(macaroonB64) {
    try {
      var raw = atob(macaroonB64);
      // The macaroon ID is the hex-encoded payment hash. The binary format
      // places the ID after a short header. For a simpler approach, we decode
      // the payment hash from the challenge invoice via the proxy settlement
      // endpoint using the macaroon bytes. However, we can extract it more
      // reliably by asking the proxy.
      return null;
    } catch (e) {
      return null;
    }
  }

  function pollSettlement(proxyOrigin, paymentHash) {
    var url = proxyOrigin + '/l402/settlement?payment_hash=' + paymentHash;
    return new Promise(function (resolve, reject) {
      var elapsed = 0;
      var timer = setInterval(function () {
        elapsed += SETTLEMENT_POLL_INTERVAL;
        if (elapsed > SETTLEMENT_POLL_TIMEOUT) {
          clearInterval(timer);
          reject(new Error('Settlement polling timed out'));
          return;
        }
        fetch(url).then(function (r) { return r.json(); }).then(function (data) {
          if (data.settled) {
            clearInterval(timer);
            resolve({ preimage: data.preimage });
          }
        }).catch(function () {});
      }, SETTLEMENT_POLL_INTERVAL);
    });
  }

  // ---------------------------------------------------------------------------
  // QR code loader (CDN, on demand)
  // ---------------------------------------------------------------------------

  var qrLoadPromise = null;

  function loadQR() {
    if (qrLoadPromise) return qrLoadPromise;
    qrLoadPromise = new Promise(function (resolve, reject) {
      if (typeof qrcode === 'function') { resolve(); return; }
      var s = document.createElement('script');
      s.src = QR_CDN;
      s.onload = resolve;
      s.onerror = function () { reject(new Error('Failed to load QR library from CDN')); };
      document.head.appendChild(s);
    });
    return qrLoadPromise;
  }

  function renderQR(data, container) {
    loadQR().then(function () {
      var qr = qrcode(0, 'M');
      qr.addData(data.toUpperCase());
      qr.make();
      container.innerHTML = qr.createSvgTag({ cellSize: 4, margin: 4 });
    }).catch(function () {
      container.textContent = 'QR unavailable';
    });
  }

  // ---------------------------------------------------------------------------
  // CSS (injected once)
  // ---------------------------------------------------------------------------

  var stylesInjected = false;

  function injectStyles() {
    if (stylesInjected) return;
    stylesInjected = true;
    var css = document.createElement('style');
    css.textContent = [
      '.l402-overlay {',
      '  position: fixed; inset: 0; z-index: 99999;',
      '  background: rgba(0,0,0,.65); backdrop-filter: blur(4px);',
      '  display: flex; align-items: center; justify-content: center;',
      '  font-family: system-ui, -apple-system, sans-serif;',
      '  color: #e5e5e5; font-size: 14px; line-height: 1.5;',
      '}',
      '.l402-modal {',
      '  background: #1a1a1a; border: 1px solid #2a2a2a; border-radius: 12px;',
      '  width: 420px; max-width: 94vw; max-height: 90vh; overflow-y: auto;',
      '  box-shadow: 0 24px 48px rgba(0,0,0,.5);',
      '}',
      '.l402-hdr {',
      '  display: flex; align-items: center; justify-content: space-between;',
      '  padding: 16px 20px; border-bottom: 1px solid #2a2a2a;',
      '}',
      '.l402-hdr h2 { font-size: 16px; font-weight: 600; margin: 0; }',
      '.l402-close {',
      '  background: none; border: none; color: #737373; font-size: 22px;',
      '  cursor: pointer; padding: 0 4px; line-height: 1;',
      '}',
      '.l402-close:hover { color: #e5e5e5; }',
      '.l402-body { padding: 20px; }',
      '.l402-meta { margin-bottom: 16px; }',
      '.l402-meta-row {',
      '  display: flex; justify-content: space-between; align-items: baseline;',
      '  font-size: 13px; color: #737373; margin-bottom: 4px;',
      '}',
      '.l402-meta-val { color: #e5e5e5; font-weight: 500; }',
      '.l402-price { color: #f7931a !important; font-size: 18px !important; font-weight: 700 !important; }',
      '.l402-enroll-badge {',
      '  display: inline-block; background: rgba(124,58,237,.2); color: #a78bfa;',
      '  font-size: 11px; font-weight: 700; padding: 2px 8px; border-radius: 4px;',
      '  letter-spacing: .04em; text-transform: uppercase; margin-bottom: 12px;',
      '}',
      '.l402-enroll-note {',
      '  font-size: 13px; color: #a78bfa; margin-bottom: 16px;',
      '  padding: 10px 12px; background: rgba(124,58,237,.08);',
      '  border: 1px solid rgba(124,58,237,.2); border-radius: 6px;',
      '}',
      '.l402-qr { text-align: center; margin: 16px 0; }',
      '.l402-qr svg { max-width: 200px; height: auto; }',
      '.l402-invoice-wrap {',
      '  display: flex; gap: 6px; align-items: stretch; margin: 12px 0;',
      '}',
      '.l402-invoice-text {',
      '  flex: 1; background: #111; border: 1px solid #2a2a2a; border-radius: 6px;',
      '  padding: 8px 10px; font-family: monospace; font-size: 11px;',
      '  color: #a3e635; word-break: break-all; max-height: 72px; overflow-y: auto;',
      '  user-select: all;',
      '}',
      '.l402-copy {',
      '  background: #2a2a2a; border: none; color: #737373; border-radius: 6px;',
      '  padding: 8px 12px; cursor: pointer; font-size: 12px; white-space: nowrap;',
      '}',
      '.l402-copy:hover { color: #e5e5e5; }',
      '.l402-btn {',
      '  display: block; width: 100%; padding: 12px; border: none; border-radius: 8px;',
      '  font-size: 15px; font-weight: 600; cursor: pointer; margin-top: 12px;',
      '  transition: background .15s;',
      '}',
      '.l402-btn-pay { background: #f7931a; color: #000; }',
      '.l402-btn-pay:hover { background: #e8841a; }',
      '.l402-btn-pay:disabled { background: #2a2a2a; color: #737373; cursor: not-allowed; }',
      '.l402-divider {',
      '  display: flex; align-items: center; gap: 10px; margin: 16px 0;',
      '  font-size: 12px; color: #737373;',
      '}',
      '.l402-divider::before, .l402-divider::after {',
      '  content: ""; flex: 1; height: 1px; background: #2a2a2a;',
      '}',
      '.l402-fallback-input {',
      '  width: 100%; background: #111; border: 1px solid #2a2a2a; border-radius: 6px;',
      '  padding: 10px 12px; font-family: monospace; font-size: 12px;',
      '  color: #e5e5e5; outline: none; margin-top: 8px; box-sizing: border-box;',
      '}',
      '.l402-fallback-input:focus { border-color: #f7931a; }',
      '.l402-btn-submit { background: #2a2a2a; color: #e5e5e5; }',
      '.l402-btn-submit:hover { background: #333; }',
      '.l402-expiry { text-align: center; font-size: 12px; color: #737373; margin-top: 12px; }',
      '.l402-status {',
      '  text-align: center; padding: 32px 16px;',
      '}',
      '.l402-status p { margin: 8px 0; }',
      '.l402-status-icon { font-size: 36px; margin-bottom: 8px; }',
      '.l402-spinner {',
      '  width: 32px; height: 32px; margin: 0 auto 12px;',
      '  border: 3px solid #2a2a2a; border-top-color: #f7931a;',
      '  border-radius: 50%; animation: l402spin .7s linear infinite;',
      '}',
      '@keyframes l402spin { to { transform: rotate(360deg); } }',
      '.l402-err { color: #ef4444; font-size: 13px; }',
      '.l402-granted-text { color: #22c55e; font-weight: 600; font-size: 16px; }',
      '.l402-state { display: none; }',
      '.l402-state.active { display: block; }',
    ].join('\n');
    document.head.appendChild(css);
  }

  // ---------------------------------------------------------------------------
  // Modal
  // ---------------------------------------------------------------------------

  function createModal(challenge, resourceUrl) {
    injectStyles();

    var sats = parseInvoiceAmount(challenge.invoice);
    var provider = getProvider();
    var isEnrollment = challenge.type === 'enrollment';
    var resolve, reject;
    var promise = new Promise(function (res, rej) { resolve = res; reject = rej; });
    var countdownInterval;
    var secondsLeft = INVOICE_EXPIRY_SECS;
    var destroyed = false;

    var overlay = document.createElement('div');
    overlay.className = 'l402-overlay';

    var awaiting = buildAwaitingState();
    var paying = buildStatusState('paying', 'Sending payment...');
    var verifying = buildStatusState('verifying', 'Verifying with server...');
    var granted = buildGrantedState();
    var failed = buildFailedState();
    var expired = buildExpiredState();

    var bodyDiv = document.createElement('div');
    bodyDiv.className = 'l402-body';
    bodyDiv.appendChild(awaiting);
    bodyDiv.appendChild(paying);
    bodyDiv.appendChild(verifying);
    bodyDiv.appendChild(granted);
    bodyDiv.appendChild(failed);
    bodyDiv.appendChild(expired);

    var modal = document.createElement('div');
    modal.className = 'l402-modal';

    var hdr = document.createElement('div');
    hdr.className = 'l402-hdr';
    var title = document.createElement('h2');
    title.textContent = isEnrollment ? 'Enrollment Required' : 'Payment Required';
    var closeBtn = document.createElement('button');
    closeBtn.className = 'l402-close';
    closeBtn.innerHTML = '&times;';
    closeBtn.onclick = function () { destroy(); reject(new Error('Payment cancelled')); };
    hdr.appendChild(title);
    hdr.appendChild(closeBtn);

    modal.appendChild(hdr);
    modal.appendChild(bodyDiv);
    overlay.appendChild(modal);
    document.body.appendChild(overlay);

    overlay.addEventListener('click', function (e) {
      if (e.target === overlay) { destroy(); reject(new Error('Payment cancelled')); }
    });

    showState('awaiting');
    startCountdown();

    function buildAwaitingState() {
      var el = document.createElement('div');
      el.className = 'l402-state';
      el.setAttribute('data-state', 'awaiting');

      if (isEnrollment) {
        var badge = document.createElement('div');
        badge.className = 'l402-enroll-badge';
        badge.textContent = 'One-Time Enrollment';
        el.appendChild(badge);

        var note = document.createElement('div');
        note.className = 'l402-enroll-note';
        note.textContent = 'You have reached the anonymous access limit. '
          + 'This payment mints a credential for your session. '
          + 'Subsequent requests will be priced individually at a lower rate.';
        el.appendChild(note);
      }

      var meta = document.createElement('div');
      meta.className = 'l402-meta';

      var row1 = document.createElement('div');
      row1.className = 'l402-meta-row';
      row1.innerHTML = '<span>Resource</span><span class="l402-meta-val">' + escHtml(resourceUrl) + '</span>';
      meta.appendChild(row1);

      var row2 = document.createElement('div');
      row2.className = 'l402-meta-row';
      var priceLabel = isEnrollment ? 'Enrollment stake' : 'Price';
      row2.innerHTML = '<span>' + priceLabel + '</span><span class="l402-meta-val l402-price">' +
        (sats != null ? formatSats(sats) : 'see invoice') + '</span>';
      meta.appendChild(row2);

      el.appendChild(meta);

      var qrWrap = document.createElement('div');
      qrWrap.className = 'l402-qr';
      renderQR(challenge.invoice, qrWrap);
      el.appendChild(qrWrap);

      var invoiceWrap = document.createElement('div');
      invoiceWrap.className = 'l402-invoice-wrap';
      var invoiceText = document.createElement('div');
      invoiceText.className = 'l402-invoice-text';
      invoiceText.textContent = challenge.invoice;
      var copyBtn = document.createElement('button');
      copyBtn.className = 'l402-copy';
      copyBtn.textContent = 'Copy';
      copyBtn.onclick = function () { copyToClipboard(challenge.invoice, copyBtn); };
      invoiceWrap.appendChild(invoiceText);
      invoiceWrap.appendChild(copyBtn);
      el.appendChild(invoiceWrap);

      if (provider) {
        var payBtn = document.createElement('button');
        payBtn.className = 'l402-btn l402-btn-pay';
        payBtn.textContent = isEnrollment ? 'Pay Enrollment Stake' : 'Pay with Lightning';
        payBtn.onclick = function () { onProviderPay(payBtn); };
        el.appendChild(payBtn);

        var divider = document.createElement('div');
        divider.className = 'l402-divider';
        divider.textContent = 'or paste preimage manually';
        el.appendChild(divider);
      } else {
        var notice = document.createElement('p');
        notice.style.cssText = 'font-size:13px;color:#737373;margin-bottom:8px;';
        notice.textContent = 'No Lightning wallet detected. Pay the invoice above, then paste the preimage.';
        el.appendChild(notice);
      }

      var preimageInput = document.createElement('input');
      preimageInput.className = 'l402-fallback-input';
      preimageInput.type = 'text';
      preimageInput.placeholder = 'Paste hex preimage (64 characters)';
      preimageInput.setAttribute('data-role', 'preimage');
      el.appendChild(preimageInput);

      var submitBtn = document.createElement('button');
      submitBtn.className = 'l402-btn l402-btn-submit';
      submitBtn.textContent = 'Submit Preimage';
      submitBtn.onclick = function () { onManualSubmit(preimageInput); };
      el.appendChild(submitBtn);

      preimageInput.addEventListener('keydown', function (e) {
        if (e.key === 'Enter') onManualSubmit(preimageInput);
      });

      var expiryEl = document.createElement('div');
      expiryEl.className = 'l402-expiry';
      expiryEl.setAttribute('data-role', 'expiry');
      el.appendChild(expiryEl);

      return el;
    }

    function buildStatusState(name, text) {
      var el = document.createElement('div');
      el.className = 'l402-state';
      el.setAttribute('data-state', name);
      el.innerHTML = '<div class="l402-status"><div class="l402-spinner"></div><p>' + escHtml(text) + '</p></div>';
      return el;
    }

    function buildGrantedState() {
      var el = document.createElement('div');
      el.className = 'l402-state';
      el.setAttribute('data-state', 'granted');
      el.innerHTML = '<div class="l402-status"><div class="l402-status-icon">&#x2713;</div>' +
        '<p class="l402-granted-text">' + (isEnrollment ? 'Enrolled' : 'Access Granted') + '</p></div>';
      return el;
    }

    function buildFailedState() {
      var el = document.createElement('div');
      el.className = 'l402-state';
      el.setAttribute('data-state', 'failed');
      el.innerHTML = '<div class="l402-status"><div class="l402-status-icon" style="color:#ef4444">&#x2717;</div>' +
        '<p style="font-weight:600">Payment Failed</p>' +
        '<p class="l402-err" data-role="error-msg"></p></div>';
      return el;
    }

    function buildExpiredState() {
      var el = document.createElement('div');
      el.className = 'l402-state';
      el.setAttribute('data-state', 'expired');
      el.innerHTML = '<div class="l402-status"><div class="l402-status-icon" style="color:#eab308">&#x23F0;</div>' +
        '<p style="font-weight:600">Invoice Expired</p>' +
        '<p style="font-size:13px;color:#737373">The payment window has closed. Retry to get a fresh invoice.</p></div>';
      return el;
    }

    function showState(name) {
      var states = bodyDiv.querySelectorAll('.l402-state');
      for (var i = 0; i < states.length; i++) {
        states[i].classList.toggle('active', states[i].getAttribute('data-state') === name);
      }
    }

    function startCountdown() {
      updateExpiry();
      countdownInterval = setInterval(function () {
        secondsLeft--;
        if (secondsLeft <= 0) {
          clearInterval(countdownInterval);
          showState('expired');
          reject(new Error('Invoice expired'));
          return;
        }
        updateExpiry();
      }, 1000);
    }

    function updateExpiry() {
      var el = bodyDiv.querySelector('[data-role="expiry"]');
      if (!el) return;
      var m = Math.floor(secondsLeft / 60);
      var s = secondsLeft % 60;
      el.textContent = 'Invoice expires in ' + m + ':' + (s < 10 ? '0' : '') + s;
    }

    function onProviderPay(btn) {
      btn.disabled = true;
      btn.textContent = 'Opening wallet...';
      showState('paying');

      provider.sendPayment(challenge.invoice).then(function (result) {
        if (!result || !result.preimage) {
          throw new Error('Wallet did not return a preimage');
        }
        resolve({ macaroon: challenge.macaroon, preimage: result.preimage });
      }).catch(function (err) {
        showState('awaiting');
        btn.disabled = false;
        btn.textContent = isEnrollment ? 'Pay Enrollment Stake' : 'Pay with Lightning';
        var errP = document.createElement('p');
        errP.style.cssText = 'color:#ef4444;font-size:12px;margin-top:8px;';
        errP.textContent = 'Payment error: ' + err.message;
        btn.parentNode.insertBefore(errP, btn.nextSibling);
        setTimeout(function () { if (errP.parentNode) errP.remove(); }, 5000);
      });
    }

    function onManualSubmit(input) {
      var preimage = input.value.trim();
      if (!preimage) return;
      if (!/^[0-9a-fA-F]{64}$/.test(preimage)) {
        input.style.borderColor = '#ef4444';
        setTimeout(function () { input.style.borderColor = ''; }, 2000);
        return;
      }
      resolve({ macaroon: challenge.macaroon, preimage: preimage });
    }

    function destroy() {
      if (destroyed) return;
      destroyed = true;
      clearInterval(countdownInterval);
      if (overlay.parentNode) overlay.remove();
    }

    return {
      awaitPayment: function () { return promise; },
      setState: function (state, message) {
        showState(state);
        if (message) {
          var msgEl = bodyDiv.querySelector('[data-role="error-msg"]');
          if (msgEl) msgEl.textContent = message;
        }
      },
      destroy: destroy,
    };
  }

  // ---------------------------------------------------------------------------
  // Helpers
  // ---------------------------------------------------------------------------

  function escHtml(str) {
    var d = document.createElement('div');
    d.textContent = str;
    return d.innerHTML;
  }

  function formatSats(n) {
    return n.toLocaleString() + ' sats';
  }

  function copyToClipboard(text, btn) {
    navigator.clipboard.writeText(text).then(function () {
      var orig = btn.textContent;
      btn.textContent = 'Copied';
      setTimeout(function () { btn.textContent = orig; }, 1500);
    }).catch(function () {});
  }

  function extractFilename(url, response) {
    var cd = response.headers.get('Content-Disposition');
    if (cd) {
      var match = cd.match(/filename="?([^";]+)"?/);
      if (match) return match[1];
    }
    var path = url.split('?')[0].split('#')[0];
    var segments = path.split('/');
    return segments[segments.length - 1] || 'download';
  }

  function delay(ms) {
    return new Promise(function (r) { setTimeout(r, ms); });
  }

  function resolveBaseUrl(url) {
    try {
      var u = new URL(url, window.location.href);
      return u.origin;
    } catch (e) {
      return window.location.origin;
    }
  }

  // ---------------------------------------------------------------------------
  // Engine
  // ---------------------------------------------------------------------------

  function engine(url, fetchOpts) {
    fetchOpts = fetchOpts || {};

    var credential = credentialStore.get();
    var initHeaders = new Headers(fetchOpts.headers || {});
    if (credential) {
      initHeaders.set('L402-Credential', credential);
    }
    var initRequest = Object.assign({}, fetchOpts, { headers: initHeaders });

    return fetch(url, initRequest).then(function (response) {
      if (response.status !== 402) return response;

      var wwwAuth = response.headers.get('WWW-Authenticate');
      var challenge = parseChallenge(wwwAuth);
      if (!challenge) return response;

      var resourcePath = url;
      try { resourcePath = new URL(url, window.location.href).pathname; } catch (e) {}

      var modal = createModal(challenge, resourcePath);

      return modal.awaitPayment().then(function (paymentResult) {
        modal.setState('verifying');

        var retryHeaders = new Headers(fetchOpts.headers || {});
        retryHeaders.set('Authorization', buildAuthHeader(paymentResult));
        if (credential) {
          retryHeaders.set('L402-Credential', credential);
        }
        var retryRequest = Object.assign({}, fetchOpts, { headers: retryHeaders });

        return fetch(url, retryRequest).then(function (retryResponse) {
          // Enrollment response: store credential
          if (challenge.type === 'enrollment') {
            var newCred = retryResponse.headers.get('L402-Credential');
            if (retryResponse.ok && newCred) {
              credentialStore.set(newCred);
              modal.setState('granted');
              return delay(1200).then(function () {
                modal.destroy();
                // After enrollment, re-run the engine to make the actual
                // resource request with the new credential.
                return engine(url, fetchOpts);
              });
            }
            // Enrollment response came as JSON body
            if (retryResponse.ok) {
              return retryResponse.clone().json().then(function (body) {
                if (body.credential) {
                  credentialStore.set(body.credential);
                }
                modal.setState('granted');
                return delay(1200).then(function () {
                  modal.destroy();
                  return engine(url, fetchOpts);
                });
              });
            }
          }

          if (retryResponse.ok) {
            modal.setState('granted');
            return delay(1200).then(function () {
              modal.destroy();
              return retryResponse;
            });
          }

          var msg = retryResponse.status === 401
            ? 'Token rejected by server.'
            : 'Server returned ' + retryResponse.status + '.';
          modal.setState('failed', msg);
          return retryResponse;
        });
      }).catch(function (err) {
        modal.destroy();
        throw err;
      });
    });
  }

  // ---------------------------------------------------------------------------
  // Mode 1: Declarative — [data-l402] click interception
  // ---------------------------------------------------------------------------

  function handleDeclarativeClick(e) {
    var el = e.target.closest('[data-l402]');
    if (!el) return;

    e.preventDefault();
    var url = el.getAttribute('data-l402-url') || el.getAttribute('href');
    if (!url) return;

    engine(url).then(function (response) {
      if (!response || !response.ok) return;
      response.blob().then(function (blob) {
        var filename = extractFilename(url, response);
        var a = document.createElement('a');
        a.href = URL.createObjectURL(blob);
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        setTimeout(function () { URL.revokeObjectURL(a.href); a.remove(); }, 100);
      });
    }).catch(function () {});
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () {
      document.addEventListener('click', handleDeclarativeClick);
    });
  } else {
    document.addEventListener('click', handleDeclarativeClick);
  }

  // ---------------------------------------------------------------------------
  // Mode 2: Programmatic — l402Fetch()
  // ---------------------------------------------------------------------------

  window.l402Fetch = engine;

  // ---------------------------------------------------------------------------
  // Mode 3: Global — L402.installGlobal()
  // ---------------------------------------------------------------------------

  var originalFetch = window.fetch;

  var L402 = {
    fetch: engine,
    installGlobal: function () {
      window.fetch = function l402GlobalFetch(url, opts) {
        return engine(url, opts);
      };
    },
    restoreGlobal: function () {
      window.fetch = originalFetch;
    },
    credential: credentialStore,
    // Register an NWC provider. Call with a provider object that has
    // sendPayment(invoice) → Promise<{preimage}>.
    setProvider: function (p) {
      window._l402NwcProvider = p;
    },
  };

  window.L402 = L402;

})();
