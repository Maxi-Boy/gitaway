// Заглушка API gitaway для проверки интерфейса в браузере без Go (только для разработки).
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';

const WEB = process.argv[2];
const PORT = Number(process.argv[3] || 8451);
const now = Math.floor(Date.now() / 1000);
const H = 3600;
let logged = false;
const ROOT = '/home/sysadmin/projects';
const repos = [
  { id: 'r1', path: ROOT + '/myrepo', remote: 'git@github.com:Maxi-Boy/myrepo.git' },
  { id: 'r2', path: ROOT + '/docs-site', remote: '' },
];
let settings = { default_folder: ROOT + '/export', open_on_start: true, ask_on_overwrite: true, move_by_default: false };
const wts = [
  { path: ROOT + '/myrepo', branch: 'main', main: true },
  { path: ROOT + '/myrepo.worktrees/feature-login', branch: 'feature/login', main: false },
];
const branches = [
  { name: 'main', remote: false, upstream: 'origin/main', ahead: 0, behind: 0, current: true },
  { name: 'feature/login', remote: false, upstream: 'origin/feature/login', ahead: 1, behind: 2, current: false },
  { name: 'fix/api-timeout', remote: false, upstream: '', ahead: 0, behind: 0, current: false },
  { name: 'origin/main', remote: true, ahead: 0, behind: 0 },
  { name: 'origin/release-2.1', remote: true, ahead: 0, behind: 0 },
];
let changes = {
  [ROOT + '/myrepo']: [],
  [ROOT + '/myrepo.worktrees/feature-login']: [
    { path: 'git/status.go', x: ' ', y: 'M' },
    { path: 'server.go', x: 'M', y: ' ' },
    { path: 'worktree.go', x: '?', y: '?' },
  ],
};
const tree = {
  '': [
    { name: 'api', is_dir: true, size: 4096, mtime: now - 2 * H },
    { name: 'auth', is_dir: true, size: 4096, mtime: now - 30 * H },
    { name: 'git', is_dir: true, size: 4096, mtime: now - 20 * H },
    { name: 'server.go', is_dir: false, size: 3990, mtime: now - 25 * H },
    { name: 'worktree.go', is_dir: false, size: 1200, mtime: now - 600 },
    { name: 'README.md', is_dir: false, size: 820, mtime: now - 90 * H },
  ],
  git: [
    { name: 'branches.go', is_dir: false, size: 2100, mtime: now - 120 * H },
    { name: 'status.go', is_dir: false, size: 1150, mtime: now - 300 },
  ],
};
const commits = {
  '': {
    api: { hash: 'a1', author: 'i.petrov', subject: 'Add pull endpoint, ff-only', time: now - 2 * H },
    auth: { hash: 'a2', author: 'sysadmin', subject: 'PAM login and session cookies', time: now - 26 * H },
    git: { hash: 'a3', author: 'sysadmin', subject: 'Parse ahead/behind counters', time: now - 22 * H },
    'server.go': { hash: 'a4', author: 'sysadmin', subject: 'Wire auth middleware', time: now - 25 * H },
    'README.md': { hash: 'a5', author: 'i.petrov', subject: 'Describe worktree tabs', time: now - 5 * 24 * H },
  },
  git: {
    'branches.go': { hash: 'b1', author: 'sysadmin', subject: 'List local and remote branches', time: now - 5 * 24 * H },
    'status.go': { hash: 'a3', author: 'sysadmin', subject: 'Parse ahead/behind counters', time: now - 22 * H },
  },
};
const files = { 'README.md': '# myrepo\n\nПример файла.\n', 'server.go': 'package main\n\nfunc main() {\n\tprintln("hi")\n}\n' };
let fileVer = 1;
const merging = {};
const forceNeeded = new Set(['fix/api-timeout']);
let myTasks = [];
let ghChecks = 0;
const ghKey = { key_id: 'acme-widgets-1', owner: 'acme', repo: 'widgets', public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleExampleExampleExampleExampleExampleEx gitaway@nvme-lab acme/widgets', keys_url: 'https://github.com/acme/widgets/settings/keys/new', title: 'gitaway@nvme-lab', transport: '' };
let trusted = false;
const repoTasks = [{ id: 'repo:0', name: 'Линтер', steps: ['golangci-lint run'] }];
const autoTasks = [{ id: 'auto:go test', name: 'go test', steps: ['go test ./...'] }, { id: 'auto:make build', name: 'make build', steps: ['make build'] }];
const fsDirs = {
  [ROOT]: [{ name: 'export', is_dir: true, size: 0, mtime: now - 50 * H }, { name: 'myrepo', is_dir: true, size: 0, mtime: now - H }],
  [ROOT + '/export']: [
    { name: 'docs', is_dir: true, size: 0, mtime: now - 200 * H },
    { name: 'server.go', is_dir: false, size: 3900, mtime: now - 240 * H },
    { name: 'README.md', is_dir: false, size: 800, mtime: now - 100 * H },
  ],
  [ROOT + '/export/docs']: [],
};

// сеансы терминала: эхо-оболочка, вывод копится для переподключения
const sessions = [];
const PROMPT = '\x1b[32msysadmin@nvme-lab\x1b[0m:\x1b[34m~/myrepo\x1b[0m$ ';

function wsFrame(op, data) {
  const len = data.length;
  const head = len < 126 ? Buffer.from([0x80 | op, len]) : Buffer.from([0x80 | op, 126, len >> 8, len & 255]);
  return Buffer.concat([head, data]);
}

function wsParse(buf) {
  const out = [];
  let i = 0;
  while (buf.length - i >= 6) {
    const op = buf[i] & 15;
    let len = buf[i + 1] & 127;
    let p = i + 2;
    if (len === 126) { len = buf.readUInt16BE(p); p += 2; }
    const mask = buf.subarray(p, p + 4);
    p += 4;
    if (buf.length < p + len) break;
    const data = Buffer.from(buf.subarray(p, p + len));
    for (let k = 0; k < len; k++) data[k] ^= mask[k % 4];
    out.push({ op, data });
    i = p + len;
  }
  return { frames: out, rest: buf.subarray(i) };
}

function json(res, code, v) {
  res.writeHead(code, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(v));
}
const err = (res, code, c, m) => json(res, code, { error: { code: c, message: m } });

function body(req) {
  return new Promise((r) => {
    let s = '';
    req.on('data', (d) => (s += d));
    req.on('end', () => r(s ? JSON.parse(s) : {}));
  });
}

function status(wt) {
  const p = wt || ROOT + '/myrepo';
  const b = wts.find((w) => w.path === p);
  const br = branches.find((x) => x.name === b?.branch);
  return { branch: b?.branch, upstream: br?.upstream, ahead: br?.ahead || 0, behind: br?.behind || 0, changes: changes[p] || [], merging: !!merging[p] };
}

http.createServer(async (req, res) => {
  const u = new URL(req.url, 'http://x');
  const q = Object.fromEntries(u.searchParams);
  const p = u.pathname;
  if (!p.startsWith('/api/')) {
    const f = path.join(WEB, p === '/' ? 'index.html' : p);
    if (!f.startsWith(WEB) || !fs.existsSync(f)) { res.writeHead(404); return res.end(); }
    const type = { '.js': 'text/javascript', '.css': 'text/css', '.html': 'text/html' }[path.extname(f)] || 'application/octet-stream';
    res.writeHead(200, { 'Content-Type': type, 'Content-Security-Policy': "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'" });
    return res.end(fs.readFileSync(f));
  }
  if (p === '/api/login') {
    const b = await body(req);
    if (b.password !== 'secret') return err(res, 401, 'invalid_credentials', 'invalid credentials');
    logged = true;
    return json(res, 200, { user: b.username, csrf: 'tok' });
  }
  if (!logged) return err(res, 401, 'unauthorized', 'login required');
  if (req.method !== 'GET' && req.headers['x-csrf-token'] !== 'tok') return err(res, 403, 'bad_csrf', 'csrf');
  const b = req.method === 'GET' ? {} : await body(req);
  switch (p) {
    case '/api/me': return json(res, 200, { user: 'sysadmin', host: 'nvme-lab', csrf: 'tok', roots: [ROOT], default_folder: settings.default_folder });
    case '/api/logout': logged = false; return json(res, 200, { ok: true });
    case '/api/repos': return json(res, 200, repos);
    case '/api/settings': if (req.method === 'PUT') settings = { ...settings, ...b }; return json(res, 200, settings);
    case '/api/worktrees': return json(res, 200, q.repo === 'r1' ? wts : [{ path: (repos.find((r) => r.id === q.repo) || repos[1]).path, branch: 'main', main: true }]);
    case '/api/branches': return json(res, 200, q.repo === 'r1' ? branches : [branches[0]]);
    case '/api/status': return json(res, 200, status(q.wt));
    case '/api/tree': {
      const list = tree[q.path || ''];
      if (!list) return err(res, 404, 'not_found', 'not found');
      const ch = changes[q.wt || ROOT + '/myrepo'] || [];
      const marks = {};
      for (const c of ch) {
        const rel = q.path ? c.path.slice(q.path.length + 1) : c.path;
        if (q.path && !c.path.startsWith(q.path + '/')) continue;
        const [n, ...rest] = rel.split('/');
        marks[n] = rest.length && c.x !== '?' ? '*' : (c.x + c.y).trim();
      }
      return json(res, 200, { path: q.path || '', entries: list.map((e) => ({ ...e, status: marks[e.name] })), truncated: false });
    }
    case '/api/commits': return setTimeout(() => json(res, 200, commits[q.path || ''] || {}), 300);
    case '/api/file':
      if (req.method === 'PUT') { files[q.path] = b.content; fileVer++; return json(res, 200, { size: b.content.length, mtime: now, version: 'v' + fileVer }); }
      if (!(q.path in files)) return json(res, 200, { path: q.path, content: '// ' + q.path + '\npackage git\n', size: 20, mtime: now, version: 'v' + fileVer });
      return json(res, 200, { path: q.path, content: files[q.path], size: files[q.path].length, mtime: now, version: 'v' + fileVer });
    case '/api/diff':
      return json(res, 200, { path: q.path, staged: q.staged === '1', untracked: q.path === 'worktree.go',
        text: `diff --git a/${q.path} b/${q.path}\nindex 1a2b3c..4d5e6f 100644\n--- a/${q.path}\n+++ b/${q.path}\n@@ -10,6 +10,8 @@ func Status() {\n \tctx := context.Background()\n-\tout, err := run(ctx, "status")\n+\tout, err := run(ctx, "status", "--porcelain=v1")\n+\tif err != nil {\n+\t\treturn nil, err\n+\t}\n \treturn parse(out)\n` });
    case '/api/fs': {
      const dir = q.dir || settings.default_folder;
      if (!fsDirs[dir]) return err(res, 403, 'outside_roots', 'outside');
      return json(res, 200, { dir, path: '', entries: fsDirs[dir], truncated: false });
    }
    case '/api/fs/mkdir': fsDirs[b.dir].push({ name: b.name, is_dir: true, size: 0, mtime: now }); fsDirs[b.dir + '/' + b.name] = []; return json(res, 201, { dir: b.dir, path: b.name });
    case '/api/copy/plan': {
      const names = new Set((fsDirs[b.dst.dir] || []).map((e) => e.name));
      const conflicts = b.items.filter((n) => names.has(n)).map((n) => ({
        rel: n, src_rel: n, src: { size: 3990, mtime: now - 25 * H, birth: now - 400 * H }, dst: { size: 3900, mtime: now - 240 * H, birth: now - 240 * H },
        newer: 'src', src_commit_time: now - 25 * H }));
      return json(res, 200, { dirs: [], files: b.items.map((n) => ({ rel: n, src_rel: n, size: 1000 })), conflicts, skipped: [], blocked: [], total_bytes: 1000 * b.items.length });
    }
    case '/api/copy': {
      const d = fsDirs[b.dst.dir];
      const res2 = { copied: [], replaced: [], renamed: [], skipped: [], failed: [], bytes: 0 };
      for (const n of b.items) {
        const dec = b.decisions?.[n];
        if (d.some((e) => e.name === n)) {
          if (dec === 'skip') res2.skipped.push({ rel: n, reason: 'skip' });
          else if (dec === 'keep_both') { d.push({ name: n + ' (2)', is_dir: false, size: 1000, mtime: now }); res2.renamed.push({ from: n, to: n + ' (2)' }); }
          else res2.replaced.push(n);
        } else { d.push({ name: n, is_dir: false, size: 1000, mtime: now }); res2.copied.push(n); }
        res2.bytes += 1000;
      }
      return json(res, 200, res2);
    }
    case '/api/github/key':
      if (!/^(https:\/\/github\.com\/)?[\w-]+\/[\w.-]+/.test(b.url)) return err(res, 400, 'bad_request', 'not a github.com repository address');
      return json(res, 200, ghKey);
    case '/api/github/check':
      ghChecks++;
      if (ghChecks === 1) return json(res, 200, { ok: false, code: 'denied', output: 'git@github.com: Permission denied (publickey).' });
      ghKey.transport = 'ssh443';
      return json(res, 200, { ok: true, transport: 'ssh443', branches: 3 });
    case '/api/github/clone': {
      if (b.key_id && !ghKey.transport) return err(res, 409, 'not_checked', 'check first');
      const info = { id: 's' + (sessions.length + 1), kind: 'clone', title: 'clone acme/widgets', dir: ROOT, repo: '', wt: '', running: true, exit_code: 0, started: new Date().toISOString(),
        buf: "Cloning into '" + b.dest + "'...\r\nReceiving objects: 100% (42/42), done.\r\n", line: '', finishAt: Date.now() + 500, tail: '',
        onDone: () => { if (!repos.some((r) => r.path === b.dest)) repos.push({ id: 'r' + (repos.length + 1), path: b.dest, remote: 'https://github.com/acme/widgets.git' }); } };
      sessions.push(info);
      const { buf, line, finishAt, tail, onDone, ...pub } = info;
      return json(res, 201, { session: pub, dest: b.dest });
    }
    case '/api/tasks':
      return json(res, 200, { mine: myTasks, auto: q.repo ? autoTasks : [], repo: q.repo ? { tasks: repoTasks, hash: 'h1', trusted, error: '' } : null });
    case '/api/tasks/mine':
      if (req.method === 'PUT') myTasks = b.map((x, i) => ({ ...x, id: x.id || 'my:' + i + Date.now() }));
      return json(res, 200, myTasks);
    case '/api/tasks/trust':
      if (b.hash !== 'h1') return err(res, 409, 'changed', 'changed');
      trusted = true;
      return json(res, 200, { trusted: true });
    case '/api/tasks/run': {
      const task = [...myTasks, ...repoTasks, ...autoTasks].find((x) => x.id === b.id);
      if (!task) return err(res, 404, 'not_found', 'task not found');
      if (task.id.startsWith('repo:') && !trusted) return err(res, 403, 'not_trusted', 'needs approval');
      const lines = task.steps.map((s, i) => `\x1b[1;36m▶ ${i + 1}/${task.steps.length}: ${s}\x1b[0m\r\nok\r\n`).join('');
      const info = { id: 's' + (sessions.length + 1), kind: 'task', title: task.name, dir: ROOT + '/myrepo', repo: b.repo, wt: b.wt, running: true, exit_code: 0, started: new Date().toISOString(), buf: lines, line: '', finishAt: Date.now() + 600, tail: '\x1b[32m✓ готово\x1b[0m\r\n' };
      sessions.push(info);
      const { buf, line, finishAt, tail, ...pub } = info;
      return json(res, 201, pub);
    }
    case '/api/sessions':
      if (req.method === 'POST') {
        const info = { id: 's' + (sessions.length + 1), title: b.repo ? 'feature-login' : 'export', dir: b.dir || ROOT + '/myrepo', running: true, exit_code: 0, buf: PROMPT, line: '' };
        sessions.push(info);
        return json(res, 201, info);
      }
      if (req.method === 'DELETE') { const i = sessions.findIndex((x) => x.id === q.id); if (i < 0) return err(res, 404, 'not_found', 'session not found'); sessions.splice(i, 1); return json(res, 200, { ok: true }); }
      return json(res, 200, sessions.map(({ buf, line, finishAt, tail, onDone, ...x }) => x));
  }
  if (p.startsWith('/api/git/')) {
    const op = p.slice(9);
    const wt = b.wt || ROOT + '/myrepo';
    if (op === 'stage') { changes[wt] = changes[wt].map((c) => (b.paths.includes(c.path) || b.paths.includes('.') ? { ...c, x: c.x === '?' ? 'A' : c.y !== ' ' ? c.y : c.x, y: ' ' } : c)); return json(res, 200, { status: status(b.wt) }); }
    if (op === 'unstage') { changes[wt] = changes[wt].map((c) => (b.paths.includes(c.path) || b.paths.includes('.') ? { ...c, x: c.x === 'A' ? '?' : ' ', y: c.x === 'A' ? '?' : c.x } : c)); return json(res, 200, { status: status(b.wt) }); }
    if (op === 'discard') { changes[wt] = changes[wt].filter((c) => !b.paths.includes(c.path)); return json(res, 200, { discarded: b.paths, status: status(b.wt) }); }
    if (op === 'commit') { changes[wt] = changes[wt].filter((c) => c.x === ' ' || c.x === '?').map((c) => c); return json(res, 200, { commit: 'abcdef1234567890abcdef1234567890abcdef12', output: '[feature/login abcdef1] ' + b.message, status: status(b.wt) }); }
    if (op === 'push') return err(res, 422, 'git_failed', 'git push: exit 1: ! [rejected] feature/login -> feature/login (non-fast-forward)');
    if (op === 'merge' && b.branch === 'origin/release-2.1') {
      merging[wt] = true;
      changes[wt] = [...changes[wt], { path: 'README.md', x: 'U', y: 'U' }];
      return err(res, 422, 'git_failed', 'git merge: exit 1: CONFLICT (content): Merge conflict in README.md');
    }
    if (op === 'merge-abort') { merging[wt] = false; changes[wt] = changes[wt].filter((c) => c.x !== 'U'); return json(res, 200, { output: '' }); }
    if (op === 'delete-branch' && !b.remote) {
      if (forceNeeded.has(b.branch) && !b.force) return err(res, 409, 'not_merged', 'branch is not fully merged: ' + b.branch);
      const i = branches.findIndex((x) => x.name === b.branch);
      if (i >= 0) branches.splice(i, 1);
      return json(res, 200, { output: 'Deleted branch ' + b.branch });
    }
    return json(res, 200, { output: op === 'fetch' ? 'From github.com:Maxi-Boy/myrepo\n   1a2b3c..4d5e6f  main -> origin/main' : 'Already up to date.' });
  }
  err(res, 404, 'not_found', 'unknown API endpoint');
}).on('upgrade', (req, sock) => {
  const u = new URL(req.url, 'http://x');
  const sess = sessions.find((x) => x.id === u.searchParams.get('id'));
  if (!sess || !req.headers.origin) { sock.end('HTTP/1.1 403 Forbidden\r\n\r\n'); return; }
  const accept = crypto.createHash('sha1').update(req.headers['sec-websocket-key'] + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64');
  sock.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ' + accept + '\r\n\r\n');
  const out = (text) => { sess.buf += text; sock.write(wsFrame(2, Buffer.from(text))); };
  sock.write(wsFrame(2, Buffer.from(sess.buf)));
  if (sess.kind === 'task' || sess.kind === 'clone') {
    const finish = () => {
      if (sess.running) { sess.running = false; sess.buf += sess.tail; if (sess.tail) sock.write(wsFrame(2, Buffer.from(sess.tail))); sess.onDone?.(); }
      sock.write(wsFrame(1, Buffer.from(JSON.stringify({ type: 'exit', code: sess.exit_code }))));
    };
    if (sess.running) setTimeout(finish, Math.max(0, sess.finishAt - Date.now())); else finish();
  }
  let pending = Buffer.alloc(0);
  sock.on('data', (chunk) => {
    const { frames, rest } = wsParse(Buffer.concat([pending, chunk]));
    pending = rest;
    for (const f of frames) {
      if (f.op === 8) { sock.end(); return; }
      if (f.op !== 2) continue; // текстовые кадры: resize
      for (const ch of f.data.toString()) {
        if (ch === '\r') {
          const cmd = sess.line.trim();
          sess.line = '';
          out('\r\n' + (cmd === 'pwd' ? sess.dir : cmd ? 'mock: ' + cmd : '') + (cmd ? '\r\n' : '') + PROMPT);
        } else if (ch === '\x7f') { if (sess.line) { sess.line = sess.line.slice(0, -1); out('\b \b'); } }
        else { sess.line += ch; out(ch); }
      }
    }
  });
  sock.on('error', () => {});
}).listen(PORT, '127.0.0.1');
