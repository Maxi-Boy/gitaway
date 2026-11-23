// Сборка web/vendor/ из исходников закреплённых версий (см. versions.txt) через esbuild.
// Запуск: node tools/vendor/build.mjs <папка с клонами> (клоны делает build.sh).
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';

// esbuild берётся из локальных node_modules или из NODE_PATH (глобальная установка)
const { build } = createRequire(import.meta.url)('esbuild');

const src = path.resolve(process.argv[2]);
const here = path.dirname(new URL(import.meta.url).pathname);
const out = path.resolve(here, '../../web/vendor');
fs.mkdirSync(out, { recursive: true });

const dir = (repo) => path.join(src, repo.replace('/', '_'));
const pkgs = {
  '@codemirror/state': dir('codemirror/state') + '/src/index.ts',
  '@codemirror/view': dir('codemirror/view') + '/src/index.ts',
  '@codemirror/commands': dir('codemirror/commands') + '/src/commands.ts',
  '@codemirror/language': dir('codemirror/language') + '/src/index.ts',
  '@codemirror/search': dir('codemirror/search') + '/src/search.ts',
  '@lezer/common': dir('lezer-parser/common') + '/src/index.ts',
  '@lezer/highlight': dir('lezer-parser/highlight') + '/src/highlight.ts',
  '@lezer/lr': dir('lezer-parser/lr') + '/src/index.ts',
  'style-mod': dir('marijnh/style-mod') + '/src/style-mod.js',
  'w3c-keyname': dir('marijnh/w3c-keyname') + '/index.js',
  crelt: dir('marijnh/crelt') + '/index.js',
  '@marijn/find-cluster-break': dir('marijnh/find-cluster-break') + '/src/index.js',
};
const xterm = dir('xtermjs/xterm.js');

// Разрешение имён пакетов в файлы клонированных исходников.
const resolver = {
  name: 'clones',
  setup(b) {
    b.onResolve({ filter: /^[@a-z]/ }, (a) => {
      if (pkgs[a.path]) return { path: pkgs[a.path] };
      if (a.path.startsWith('@codemirror/legacy-modes/')) {
        return { path: path.join(dir('codemirror/legacy-modes'), a.path.slice('@codemirror/legacy-modes/'.length)) };
      }
      for (const [prefix, base] of [['xterm-src/', xterm + '/src/'], ['xterm-addons/', xterm + '/addons/'],
        ['common/', xterm + '/src/common/'], ['browser/', xterm + '/src/browser/'], ['vs/', xterm + '/src/vs/']]) {
        if (a.path.startsWith(prefix)) return { path: resolveTs(base + a.path.slice(prefix.length)) };
      }
      return undefined;
    });
  },
};

function resolveTs(p) {
  for (const ext of ['', '.ts', '.js', '/index.ts']) if (fs.existsSync(p + ext) && fs.statSync(p + ext).isFile()) return p + ext;
  return p;
}

const common = { bundle: true, format: 'esm', minify: true, target: 'es2020', legalComments: 'none', plugins: [resolver], logLevel: 'warning' };
await build({ ...common, entryPoints: [path.join(here, 'codemirror.entry.js')], outfile: path.join(out, 'codemirror.js') });
// xterm.js написан с декораторами TypeScript (внедрение зависимостей), как в его tsconfig
await build({
  ...common, entryPoints: [path.join(here, 'xterm.entry.js')], outfile: path.join(out, 'xterm.js'),
  tsconfigRaw: { compilerOptions: { experimentalDecorators: true, useDefineForClassFields: false } },
});
fs.copyFileSync(path.join(xterm, 'css/xterm.css'), path.join(out, 'xterm.css'));

// Лицензии: все библиотеки под MIT, текст каждой рядом со сборкой.
const lic = [];
for (const line of fs.readFileSync(path.join(here, 'versions.txt'), 'utf8').split('\n')) {
  const m = /^([\w.-]+\/[\w.-]+) (\S+)$/.exec(line.trim());
  if (!m) continue;
  const f = ['LICENSE', 'LICENSE.md', 'LICENSE.txt'].map((n) => path.join(dir(m[1]), n)).find((n) => fs.existsSync(n));
  lic.push(`== ${m[1]} ${m[2]} (https://github.com/${m[1]})\n\n${f ? fs.readFileSync(f, 'utf8').trim() : 'MIT'}\n`);
}
fs.writeFileSync(path.join(out, 'LICENSES.txt'), lic.join('\n'));
for (const f of fs.readdirSync(out)) console.log(f, fs.statSync(path.join(out, f)).size);
