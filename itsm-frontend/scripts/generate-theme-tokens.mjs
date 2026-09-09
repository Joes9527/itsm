import { readFile, writeFile } from 'node:fs/promises';

const source = new URL('../src/design-system/theme-tokens.json', import.meta.url);
const destination = new URL('../src/styles/generated-theme-tokens.css', import.meta.url);
const tokens = JSON.parse(await readFile(source, 'utf8'));
const prefix = (name, values) =>
  Object.fromEntries(Object.entries(values).map(([key, value]) => [`--${name}-${key}`, value]));
const common = {
  ...tokens.common,
  ...prefix('color-primary', tokens.brand.palette),
  ...prefix('font-size', tokens.typography.fontSize),
  '--font-family-base': tokens.typography.fontFamily,
  '--font-size-page-title': tokens.typography.pageTitle,
  '--font-size-card-title': tokens.typography.cardTitle,
  '--font-size-helper': tokens.typography.helper,
  '--header-height': `${tokens.sizes.header}px`,
  '--sidebar-width': `${tokens.sizes.sidebar}px`,
  '--sidebar-collapsed-width': `${tokens.sizes.sidebarCollapsed}px`,
  '--control-height': `${tokens.sizes.button}px`,
  '--control-height-sm': `${tokens.sizes.buttonSmall}px`,
  '--card-padding': `${tokens.sizes.cardPadding}px`,
  ...Object.fromEntries(
    Object.entries(tokens.aliases).map(([key, value]) => [key, `var(${value})`])
  ),
};
const block = (selector, variables) =>
  `${selector} {\n${Object.entries(variables)
    .sort(([a], [b]) => a.localeCompare(b, 'en'))
    .map(([key, value]) => `  ${key}: ${value};`)
    .join('\n')}\n}\n`;
const output =
  '/* Generated from theme-tokens.json. Run npm run theme:generate; do not edit. */\n' +
  block(':root', { ...common, ...tokens.themes.light }) +
  '\n' +
  block('.dark', tokens.themes.dark);
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
