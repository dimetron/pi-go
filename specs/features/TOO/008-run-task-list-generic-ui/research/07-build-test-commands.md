# Build/Test Commands

**Source:** `package.json`, lines 36–47

## npm scripts

| Command                  | Script                                       | Description                                  |
|--------------------------|----------------------------------------------|----------------------------------------------|
| `npm run dev`            | `tsx src/index.ts`                           | Run in dev mode via tsx                      |
| `npm run build`          | `tsup`                                       | Build with tsup (config in `tsup.config.ts`) |
| `npm start`              | `node dist/index.js`                         | Run built output                             |
| `npm run format`         | `prettier . --write`                         | Format all files                             |
| `npm run typecheck`      | `tsc --noEmit`                               | Type-check without emitting                  |
| `npm run lint`           | `eslint .`                                   | Lint all files                               |
| `npm test`               | `node --import tsx --test test/**/*.test.ts` | Run all tests                                |
| `npm run smoke`          | `node scripts/smoke-acp.mjs`                 | Run smoke tests                              |
| `npm run prepack`        | `npm run build`                              | Pre-publish build                            |
| `npm run prepublishOnly` | `npm run test && npm run build`              | Pre-publish test + build                     |

## Key Details

- **Node requirement:** `>=20`
- **Module type:** ESM (`"type": "module"`)
- **Test runner:** Node.js built-in test runner (`node:test`) with tsx for TypeScript transpilation
- **Test glob:** `test/**/*.test.ts` — includes both `test/unit/*.test.ts` and `test/component/*.test.ts`
- **Build tool:** tsup (config in `tsup.config.ts`)
- **TypeScript config:** `tsconfig.json`
- **Lint config:** `eslint.config.js`
- **Format config:** `.prettierrc.mjs`

## Dependencies

- `@agentclientprotocol/sdk`: `^0.26.0`
- `zod`: `^3.25.0`

## Dev Dependencies

- `@eslint/js`, `eslint`, `typescript-eslint`
- `@types/node`
- `prettier`
- `tsup`, `tsx`, `typescript`
- `globals`