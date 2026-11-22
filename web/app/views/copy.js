// Копирование из ветки в папку сервера: план, диалог конфликтов, выполнение.
// Сравниваются размер, дата изменения, дата создания и дата коммита источника.
import { h, mount, dialog, toast, size, stampYY, fullStamp } from '../ui.js';
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
      const res = await conflictDialog(plan, req);
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

// conflictDialog показывает конфликты по одному, как в макете: сравнение «ветка ↔ сервер»
// по размеру и датам, решение для этого файла или сразу для всех оставшихся.
// Возвращает решения {rel: action} или null при отмене.
function conflictDialog(plan, req) {
  return new Promise((resolve) => {
    const list = plan.conflicts;
    const decisions = {};
    let i = 0;
    const box = h('div', {});
    const where = t('copy.where', {
      src: state.wt ? (state.wt.branch || state.wt.path.split('/').pop()) : '',
      dst: req.dst.dir + (req.dst.path ? '/' + req.dst.path : ''),
    });
    let d = null;

    const finish = () => d.close(decisions);
    const decide = (action) => {
      decisions[list[i].rel] = action;
      if (++i >= list.length) finish();
      else render();
    };
    const decideRest = (fn) => {
      for (let k = i; k < list.length; k++) decisions[list[k].rel] = fn(list[k]);
      finish();
    };

    const render = () => {
      const c = list[i];
      const newer = (side) => (c.newer === side ? h('span', { class: 'newer' }, ' · ' + t('copy.newer')) : null);
      const row = (label, a, b) => [h('span', { class: 'muted' }, label), h('span', {}, a), h('span', {}, b)];
      const when = (unix) => h('span', { title: fullStamp(unix) }, stampYY(unix));
      const noBirth = !c.src.birth || !c.dst.birth;
      const left = list.length - i;
      mount(box,
        h('div', { class: 'muted cmp-sub' },
          h('b', { class: 'mono', style: { color: 'var(--text)' } }, c.rel), ' · ',
          t('copy.nOfM', { i: i + 1, n: list.length }), ' · ', where),
        c.same_file ? h('p', { class: 'notice' }, t('copy.sameFile')) : null,
        h('div', { class: 'cmp' },
          h('span', {}), h('span', { class: 'hd' }, t('copy.source')), h('span', { class: 'hd' }, t('copy.target')),
          row(t('copy.size'), size(c.src.size), size(c.dst.size)),
          row(t('copy.mtime'), [when(c.src.mtime), newer('src')], [when(c.dst.mtime), newer('dst')]),
          row(t('copy.birth'), when(c.src.birth), when(c.dst.birth)),
          row(t('copy.commit'), when(c.src_commit_time), h('span', { class: 'muted' }, '—'))),
        noBirth ? h('p', { class: 'muted cmp-note' }, t('copy.birthNote')) : null,
        plan.blocked?.length && i === 0 ? h('p', { class: 'muted cmp-note' }, t('copy.blockedNote', { n: plan.blocked.length })) : null,
        h('div', { class: 'cmp-acts' },
          h('button', { class: 'btn primary', onclick: () => decide('replace') }, t('copy.replace')),
          h('button', { class: 'btn', onclick: () => decide('skip') }, t('copy.skip')),
          h('button', { class: 'btn', onclick: () => decide('keep_both') }, t('copy.keepBoth')),
          h('span', { class: 'grow' }),
          i > 0 ? h('button', { class: 'btn', onclick: () => { i--; render(); } }, t('copy.back')) : null,
          h('button', { class: 'btn', onclick: () => d.close(null) }, t('common.cancel'))),
        left > 1 ? h('div', { class: 'cmp-all' },
          h('div', { class: 'muted' }, t('copy.forRest', { n: left })),
          h('div', { class: 'cmp-acts' },
            h('button', { class: 'btn', onclick: () => decideRest(() => 'replace') }, t('copy.replaceAll')),
            h('button', { class: 'btn', onclick: () => decideRest((x) => (x.newer === 'src' ? 'replace' : 'skip')) }, t('copy.replaceNewer')),
            h('button', { class: 'btn', onclick: () => decideRest(() => 'skip') }, t('copy.skipAll')),
            h('button', { class: 'btn', onclick: () => decideRest(() => 'keep_both') }, t('copy.keepBothAll')))) : null);
      box.querySelector('.btn.primary')?.focus();
    };

    d = dialog({
      title: req.move ? t('copy.titleMove') : t('copy.titleCopy'), body: box, mid: true,
      onClose: (v) => resolve(v && typeof v === 'object' ? v : null),
    });
    render();
  });
}
