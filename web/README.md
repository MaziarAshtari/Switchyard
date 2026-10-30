# Switchyard web GUI

React + TypeScript + Vite SPA, embedded into the Go binary from `web/dist` (see `embed.go`).
Develop with `pnpm dev` (proxies `/api`, `/v1`, `/healthz` to `localhost:8080`); build with `pnpm build`.
After `pnpm dlx shadcn@latest add <component>`, check that the new file imports `cn` from `@/lib/utils` (the CLI may write `"cn"`).
