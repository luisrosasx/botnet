# Panel/panel publico (BLUEPRINT parrafo 3 y 13). Esqueleto Next.js 15 + pnpm.
FROM node:24-alpine AS build
WORKDIR /app
RUN corepack enable
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
RUN pnpm build

FROM node:24-alpine
WORKDIR /app
RUN corepack enable
COPY --from=build /app ./
EXPOSE 3000
CMD ["pnpm", "start"]
