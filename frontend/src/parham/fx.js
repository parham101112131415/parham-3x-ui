/* ── Parham FX: click sound effects + sparks ─────────────────────────
   Ported from parham's own panel theme (parham-fx) to the 3x-ui React
   frontend. Sounds: Kenney "Interface Sounds" (CC0), bundled by Vite
   from src/assets/sfx/. Scope: tap blip + .ogg bank (toggle/open/close/
   success/error/notify/click) and the 9-spark burst on buttons.
   Nothing else. Modal/toast hooks are tuned to Ant Design's DOM
   (.ant-modal-root, .ant-drawer, .ant-message-notice). */
import clickUrl from '../assets/sfx/click.ogg';
import closeUrl from '../assets/sfx/close.ogg';
import errorUrl from '../assets/sfx/error.ogg';
import notifyUrl from '../assets/sfx/notify.ogg';
import openUrl from '../assets/sfx/open.ogg';
import successUrl from '../assets/sfx/success.ogg';
import toggleUrl from '../assets/sfx/toggle.ogg';

const SOUNDS = {
  click: clickUrl,
  close: closeUrl,
  error: errorUrl,
  notify: notifyUrl,
  open: openUrl,
  success: successUrl,
  toggle: toggleUrl,
};

(function () {
  'use strict';
  try {
    function playSfx(name, vol) {
      try {
        const src = SOUNDS[name];
        if (!src) return;
        const a = new Audio(src);
        a.volume = vol || 0.4;
        const p = a.play();
        if (p && typeof p.catch === 'function') p.catch(function () {});
      } catch (e) { /* ignore */ }
    }

    /* Subtle tactile blip on real taps — never on scroll touches. */
    let audioCtx = null;
    function blip(soft) {
      try {
        if (!audioCtx) audioCtx = new (window.AudioContext || window.webkitAudioContext)();
        if (audioCtx.state === 'suspended') audioCtx.resume();
        const t = audioCtx.currentTime + 0.01;
        const o = audioCtx.createOscillator();
        const g = audioCtx.createGain();
        o.type = 'square';
        o.frequency.setValueAtTime(soft ? 520 : 760, t);
        o.frequency.exponentialRampToValueAtTime(soft ? 390 : 1180, t + 0.055);
        g.gain.setValueAtTime(soft ? 0.028 : 0.045, t);
        g.gain.exponentialRampToValueAtTime(0.0001, t + 0.075);
        o.connect(g);
        g.connect(audioCtx.destination);
        o.start(t);
        o.stop(t + 0.08);
      } catch (e) { /* ignore */ }
    }

    function reducedMotion() {
      return typeof window.matchMedia === 'function' &&
        window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    }

    function spawnSparks(el, x, y) {
      for (let i = 0; i < 9; i++) {
        const a = (Math.PI * 2 * i) / 9 + Math.random() * 0.5;
        const d = 28 + Math.random() * 42;
        const spark = document.createElement('span');
        spark.className = 'px-spark';
        spark.style.left = x + 'px';
        spark.style.top = y + 'px';
        spark.style.setProperty('--dx', (Math.cos(a) * d) + 'px');
        spark.style.setProperty('--dy', (Math.sin(a) * d) + 'px');
        el.appendChild(spark);
        (function (sp) {
          sp.addEventListener('animationend', function () { sp.remove(); }, { once: true });
          setTimeout(function () { if (sp.parentNode) sp.remove(); }, 900);
        })(spark);
      }
    }

    function ensureSparkScope(el) {
      if (el instanceof HTMLElement && !el.classList.contains('px-needs-spark')) {
        el.classList.add('px-needs-spark');
      }
    }

    function closestInteractive(target) {
      if (!(target instanceof Element) || typeof target.closest !== 'function') return null;
      return target.closest('button, a, [role="button"], [role="tab"], [role="menuitem"], [role="option"], summary, input[type="checkbox"], input[type="radio"], select');
    }

    /* ── tap-verified feedback (silent on scroll) ── */
    const taps = new Map();
    document.addEventListener('pointerdown', function (e) {
      if (e.isPrimary === false) return;
      const el = closestInteractive(e.target);
      if (!el) return;
      taps.set(e.pointerId, { x: e.clientX, y: e.clientY, t: Date.now(), el: el, moved: false });
      if (el instanceof HTMLElement && (el.tagName === 'BUTTON' || el.classList.contains('button'))) {
        ensureSparkScope(el);
      }
    }, true);
    document.addEventListener('pointermove', function (e) {
      const r = taps.get(e.pointerId);
      if (!r) return;
      if (Math.abs(e.clientX - r.x) + Math.abs(e.clientY - r.y) > 12) r.moved = true;
    }, true);
    document.addEventListener('pointerup', function (e) {
      const r = taps.get(e.pointerId);
      if (!r) return;
      taps.delete(e.pointerId);
      if (r.moved || Date.now() - r.t > 600) return;
      const cb = e.target instanceof Element ? e.target.closest('input[type="checkbox"],input[type="radio"]') : null;
      const sw = e.target instanceof Element ? e.target.closest('[role="switch"]') : null;
      if (cb || sw) { playSfx('toggle', 0.3); return; }
      const btn = r.el.closest('button, .button');
      if (btn) {
        blip(false);
        try { if (navigator.vibrate) navigator.vibrate(8); } catch (err) { /* ignore */ }
        if (!reducedMotion()) {
          const rc = btn.getBoundingClientRect();
          const cx = (e.clientX || rc.left + rc.width / 2) - rc.left;
          const cy = (e.clientY || rc.top + rc.height / 2) - rc.top;
          ensureSparkScope(btn);
          spawnSparks(btn, cx, cy);
        }
      } else {
        playSfx('click', 0.35);
      }
    }, true);
    document.addEventListener('pointercancel', function (e) { taps.delete(e.pointerId); }, true);

    /* ── select/checkbox change → toggle sound ── */
    document.addEventListener('change', function (e) {
      const t = e.target;
      if (t instanceof Element && t.matches('input[type="checkbox"],input[type="radio"],select')) {
        playSfx('toggle', 0.3);
      }
    }, true);

    /* ── modals / drawers (open/close) + message toasts (success/error/
       notify), Ant Design DOM edition ──
       antd renders modals and drawers in portals under <body>: a fresh
       .ant-modal-root / .ant-drawer node appears on open and is removed
       after the close animation; kept-mounted modals instead flip the
       wrap's inline display. Message notices land as .ant-message-notice
       with a type class. */
    const countedOpen = new WeakSet();
    const wrapState = new WeakMap();

    function eachMatch(node, selector, fn) {
      if (!(node instanceof Element)) return;
      if (node.matches(selector)) fn(node);
      node.querySelectorAll(selector).forEach(fn);
    }

    function markOpened(el) {
      if (countedOpen.has(el)) return;
      countedOpen.add(el);
      playSfx('open', 0.35);
    }

    function markClosed(el) {
      if (!countedOpen.has(el)) return;
      countedOpen.delete(el);
      playSfx('close', 0.4);
    }

    function scanAdded(node) {
      eachMatch(node, '.ant-modal-root, .ant-drawer', markOpened);
      eachMatch(node, '.ant-message-notice', function (el) {
        if (el.classList.contains('ant-message-success')) playSfx('success', 0.4);
        else if (el.classList.contains('ant-message-error')) playSfx('error', 0.4);
        else playSfx('notify', 0.35);
      });
      eachMatch(node, '.ant-notification-notice', function () { playSfx('notify', 0.35); });
    }

    function scanRemoved(node) {
      eachMatch(node, '.ant-modal-root, .ant-drawer', markClosed);
    }

    function checkWrap(el) {
      if (!(el instanceof Element) || !el.classList || !el.classList.contains('ant-modal-wrap')) return;
      const root = el.closest('.ant-modal-root') || el;
      const hidden = el.style.display === 'none';
      const was = wrapState.get(el);
      if (was === undefined) { wrapState.set(el, hidden); if (!hidden) markOpened(root); return; }
      if (was && !hidden) markOpened(root);
      if (!was && hidden) markClosed(root);
      wrapState.set(el, hidden);
    }

    function startObserver() {
      if (!document.body) return;
      const obs = new MutationObserver(function (muts) {
        for (let i = 0; i < muts.length; i++) {
          const m = muts[i];
          if (m.type === 'childList') {
            m.addedNodes.forEach(scanAdded);
            m.removedNodes.forEach(scanRemoved);
          } else if (m.type === 'attributes') {
            checkWrap(m.target);
          }
        }
      });
      obs.observe(document.body, {
        childList: true,
        subtree: true,
        attributes: true,
        attributeFilter: ['style'],
      });
    }

    if (document.body) startObserver();
    else document.addEventListener('DOMContentLoaded', startObserver, { once: true });
  } catch (e) { /* FX must never break the panel */ }
})();
