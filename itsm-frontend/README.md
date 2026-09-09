# ITSM Frontend (Next.js)

## Quick Start

1. Node 22+
2. Install deps:

```
npm install
```

3. Configure env:

```
cp .env.example .env.local
# Local direct-backend development may set NEXT_PUBLIC_API_URL=http://localhost:8090.
# Production should leave it empty so /api/v1/* stays same-origin behind Nginx.
```

4. Dev:

```
npm run dev
```

## Testing

- Type check: `npm run type-check`
- Unit/RTL: `npm run test:unit`
- Integration: `npm run test:integration`
- E2E/Playwright: `npm run test:e2e`

## Theme token workflow

[`src/design-system/theme-tokens.json`](src/design-system/theme-tokens.json) is the authoritative source for theme colors, sizes, and CSS variables. After changing it or [`src/design-system/expand-theme-tokens.mjs`](src/design-system/expand-theme-tokens.mjs), regenerate the committed CSS artifact:

```bash
npm run theme:generate
```

Commit the source and regenerated `src/styles/generated-theme-tokens.css` together. Do not edit the generated CSS directly. `npm run theme:check` fails when the artifact is stale; it also runs automatically before type-checking. Development and production builds regenerate the artifact through their existing npm pre-hooks.

## Lint/Format

```
npm run lint
npm run lint:check
```

## Production build

```bash
npm run build
test -f .next/standalone/server.js
```

The browser-facing API base is empty by default. Requests already include
`/api/v1`, while server-side proxying uses `ITSM_BACKEND_URL`.
