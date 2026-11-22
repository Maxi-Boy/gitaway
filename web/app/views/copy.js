// Копирование из ветки в папку сервера: план, диалог конфликтов, выполнение.
// Сравниваются размер, дата изменения, дата создания и дата коммита источника.
import { h, dialog, toast, size, fullStamp } from '../ui.js';
import { t } from '../i18n.js';
import { api } from '../api.js';
import { state, bus, emit, wtParam, addLog } from '../store.js';
import { run } from '../commands.js';

// serverTarget возвращает текущую папку серверной панели; без панели открывает её.
async function serverTarget() {
  if (!state.fsOpen) {
    const loaded = new Promise((resolve) => {
      bus.addEventListener('fs-loc', resolve, { once: true });
      setTimeout(resolve, 5000);
    });
    await run('fs.toggle');
    await loaded;
  }
  const loc = state.fsLoc;
  if (loc?.dir) return { dir: loc.dir, path: loc.path || '' };
  return { dir: state.settings?.default_folder || state.me?.roots?.[0] || '', path: '' };
}

// copyToServer копирует элементы items из папки path активной вкладки.
export async function copyToServer({ path, items, dst, move }) {
  const target = dst || await serverTarget();
  if (!target.dir) return toast(t('copy.noTarget'), { bad: true });
  const mv = move ?? !!state.settings?.move_by_default;
  return runCopy({
    src: { repo: state.repo.id, wt: wtParam(), path },
    items, dst: target, move: mv,
  });
}

export async function runCopy(req) {
  let plan;
  try {
    plan = await api.post('/api/copy/plan', req);
  } catch (err) {
    return toast(err.message, { bad: true });
  }
  let decisions = {};
  let policy = '';
  if (plan.conflicts?.length) {
    if (state.settings && !state.settings.ask_on_overwrite) policy = 'replace_all';
    else {
      const res = await conflictDialog(plan, req.move);
      if (!res) return;
      decisions = res;
    }
  } else if (plan.blocked?.length && !plan.files?.length) {
    return toast(t('copy.allBlocked', { n: plan.blocked.length }), { bad: true });
  }
  try {
    const res = await api.post('/api/copy', { ...req, decisions, policy });
    const n = (res.copied?.length || 0) + (res.replaced?.length || 0) + (res.renamed?.length || 0);
    const lines = [
      ...(res.replaced || []).map((p) => t('copy.replaced') + ': ' + p),
      ...(res.renamed || []).map((r) => t('copy.renamed') + ': ' + r.from + ' → ' + r.to),
      ...(res.skipped || []).map((s) => t('copy.skipped') + ': ' + s.rel + (s.reason ? ' (' + s.reason + ')' : '')),
      ...(res.failed || []).map((f) => t('copy.failed') + ': ' + f.rel + ' — ' + f.error),
      ...(plan.blocked || []).map((b) => t('copy.blocked') + ': ' + b.rel + ' — ' + b.reason),
    ];
    const ok = !(res.failed?.length);
    addLog({ cmd: (req.move ? t('copy.moveCmd') : t('copy.copyCmd')) + ' → ' + req.dst.dir + (req.dst.path ? '/' + req.dst.path : ''), ok,
      output: t('copy.summary', { n, size: size(res.bytes) }) + (lines.length ? '\n' + lines.join('\n') : '') });
    toast(t('copy.summary', { n, size: size(res.bytes) }) + (res.failed?.length ? ' · ' + t('copy.withErrors', { n: res.failed.length }) : ''), { bad: !ok });
  } catch (err) {
    toast(err.message, { bad: true });
  }
  emit('changed');
}

// conflictDialog возвращает решения {rel: action} или null при отмене.
function conflictDialog(plan, move) {
  return new Promise((resolve) => {
    const selects = [];
    const meta = (label, value, mark) => h('div', { class: 'meta' }, label + ': ', h('b', { class: mark ? 'newer' : null }, value));
    const rows = plan.conflicts.map((c) => {
      const sel = h('select', { 'aria-label': t('copy.decisionFor', { path: c.rel }) },
        h('option', { value: 'replace' }, t('copy.replace')),
        h('option', { value: 'skip' }, t('copy.skip')),
        h('option', { value: 'keep_both' }, t('copy.keepBoth')));
      sel.value = c.same_file ? 'skip' : 'replace';
      selects.push({ sel, c });
      return [
        h('div', {}, h('div', { class: 'ellipsis mono', title: c.rel }, c.rel),
          c.same_file ? h('div', { class: 'meta' }, t('copy.sameFile')) : null,
          h('div', { class: 'meta' }, c.newer === 'src' ? t('copy.srcNewer') : c.newer === 'dst' ? t('copy.dstNewer') : t('copy.sameTime'))),
        h('div', { class: 'wide-only' },
          meta(t('copy.size'), size(c.src.size)),
          meta(t('copy.mtime'), fullStamp(c.src.mtime), c.newer === 'src'),
          meta(t('copy.birth'), fullStamp(c.src.birth)),
          c.src_commit_time ? meta(t('copy.commit'), fullStamp(c.src_commit_time)) : null),
        h('div', { class: 'wide-only' },
          meta(t('copy.size'), size(c.dst.size)),
          meta(t('copy.mtime'), fullStamp(c.dst.mtime), c.newer === 'dst'),
          meta(t('copy.birth'), fullStamp(c.dst.birth))),
        h('div', {}, sel),
      ];
    });
    const setAll = (fn) => () => { for (const s of selects) s.sel.value = fn(s.c); };
    const body = [
      h('p', {}, t('copy.conflictsIntro', { n: plan.conflicts.length, total: plan.files.length })),
      plan.blocked?.length ? h('p', { class: 'muted' }, t('copy.blockedNote', { n: plan.blocked.length })) : null,
      h('div', { class: 'field', style: { flexWrap: 'wrap', marginBottom: '10px' } },
        h('button', { class: 'btn small', onclick: setAll(() => 'replace') }, t('copy.replaceAll')),
        h('button', { class: 'btn small', onclick: setAll((c) => (c.newer === 'src' ? 'replace' : 'skip')) }, t('copy.replaceNewer')),
        h('button', { class: 'btn small', onclick: setAll(() => 'skip') }, t('copy.skipAll')),
        h('button', { class: 'btn small', onclick: setAll(() => 'keep_both') }, t('copy.keepBothAll'))),
      h('div', { class: 'conf' },
        h('div', { class: 'hd' }, t('copy.file')), h('div', { class: 'hd wide-only' }, t('copy.source')),
        h('div', { class: 'hd wide-only' }, t('copy.target')), h('div', { class: 'hd' }, t('copy.action')),
        rows),
    ];
    dialog({
      title: move ? t('copy.titleMove') : t('copy.titleCopy'), wide: true, body,
      buttons: [{ label: t('common.cancel'), value: null },
        { label: move ? t('copy.runMove') : t('copy.runCopy'), primary: true, run: (close) => {
          const d = {};
          for (const s of selects) d[s.c.rel] = s.sel.value;
          close(d);
        } }],
      onClose: (v) => resolve(v && typeof v === 'object' ? v : null),
    });
  });
}
