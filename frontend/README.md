# Meridian Operations Console

Frontend for the Meridian Operations Console. Built with Vite, React 19, and TypeScript.

## Development

```bash
npm install
npm run dev
```

## Commands

| Command | Description |
|---------|-------------|
| `npm run dev` | Start development server |
| `npm run build` | Build for production |
| `npm run test` | Run unit tests |
| `npm run test:coverage` | Run tests with coverage |
| `npm run lint` | Lint TypeScript files |
| `npm run typecheck` | Type-check without emitting |
| `npm run generate` | Generate API clients from proto definitions |

## Stack

- **Framework:** React 19 + TypeScript (strict mode)
- **Build:** Vite 7
- **Testing:** Vitest + React Testing Library
- **Linting:** ESLint + Prettier
- **API:** Connect-ES via `buf generate`
