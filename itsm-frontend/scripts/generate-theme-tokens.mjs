import { readFile, writeFile } from 'node:fs/promises';
import { expandThemeTokens } from '../src/design-system/expand-theme-tokens.mjs';

const source = new URL('../src/design-system/theme-tokens.json', import.meta.url);
const destination = new URL('../src/styles/generated-theme-tokens.css', import.meta.url);
const tokens = JSON.parse(await readFile(source, 'utf8'));
const light = expandThemeTokens(tokens, false);
const dark = expandThemeTokens(tokens, true);
// Only emit changed declarations in .dark; inherited common values remain in :root.
const darkOverrides = Object.fromEntries(
  Object.entries(dark).filter(([key, value]) => light[key] !== value)
);
const block = (selector, variables) =>
  `${selector} {\n${Object.entries(variables)
    .sort(([a], [b]) => a.localeCompare(b, 'en'))
    .map(([key, value]) => `  ${key}: ${value};`)
    .join('\n')}\n}\n`;
const output =
  '/* Generated from theme-tokens.json. Run npm run theme:generate; do not edit. */\n' +
  block(':root', light) +
  '\n' +
  block('.dark', darkOverrides);
if (process.argv.includes('--check')) {
  const existing = await readFile(destination, 'utf8').catch(() => '');
  if (existing !== output) {
    console.error('Theme tokens are stale. Run npm run theme:generate.');
    process.exitCode = 1;
  } else console.log('Theme tokens are up to date.');
} else {
  await writeFile(destination, output);
  console.log('Generated theme tokens.');
}
