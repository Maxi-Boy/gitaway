// Что из CodeMirror 6 нужно редактору gitaway. Собирается в web/vendor/codemirror.js.
export { EditorState, Compartment } from '@codemirror/state';
export {
  EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter,
  drawSelection, highlightSpecialChars, rectangularSelection, dropCursor,
} from '@codemirror/view';
export { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
export {
  StreamLanguage, syntaxHighlighting, HighlightStyle, indentOnInput, bracketMatching,
  foldGutter, foldKeymap, indentUnit,
} from '@codemirror/language';
export { search, searchKeymap, highlightSelectionMatches, gotoLine } from '@codemirror/search';
export { tags } from '@lezer/highlight';

// Подсветка языков: legacy-modes не требуют генерации парсеров при сборке.
export { go } from '@codemirror/legacy-modes/mode/go.js';
export { python } from '@codemirror/legacy-modes/mode/python.js';
export { shell } from '@codemirror/legacy-modes/mode/shell.js';
export { yaml } from '@codemirror/legacy-modes/mode/yaml.js';
export { toml } from '@codemirror/legacy-modes/mode/toml.js';
export { css } from '@codemirror/legacy-modes/mode/css.js';
export { xml, html } from '@codemirror/legacy-modes/mode/xml.js';
export { standardSQL, pgSQL, mySQL } from '@codemirror/legacy-modes/mode/sql.js';
export { dockerFile } from '@codemirror/legacy-modes/mode/dockerfile.js';
export { nginx } from '@codemirror/legacy-modes/mode/nginx.js';
export { rust } from '@codemirror/legacy-modes/mode/rust.js';
export { c, cpp, java, csharp, kotlin } from '@codemirror/legacy-modes/mode/clike.js';
export { javascript, json, typescript } from '@codemirror/legacy-modes/mode/javascript.js';
export { diff } from '@codemirror/legacy-modes/mode/diff.js';
export { properties } from '@codemirror/legacy-modes/mode/properties.js';
export { lua } from '@codemirror/legacy-modes/mode/lua.js';
export { perl } from '@codemirror/legacy-modes/mode/perl.js';
export { ruby } from '@codemirror/legacy-modes/mode/ruby.js';
export { powerShell } from '@codemirror/legacy-modes/mode/powershell.js';
export { protobuf } from '@codemirror/legacy-modes/mode/protobuf.js';
