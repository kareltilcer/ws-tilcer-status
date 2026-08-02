# syntax=docker/dockerfile:1
#
# Static SPA image for the `status` frontend, mirroring home/fin: build the Vite
# bundle, then serve it with Nginx. The client is hardwired same-origin (relative
# /api paths — see src/api/client.ts). Which Nginx config is baked is chosen by the
# NGINX_CONF build arg (static-only for the two-apps model; proxying for the
# compose harness).
#
# Build context is ./frontend (see docker-compose.yml).

# ── Stage 1: build the SPA ─────────────────────────────────────────────
FROM node:24-alpine AS build
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY . .
RUN npm run build            # emits /app/dist

# ── Stage 2: serve with Nginx ──────────────────────────────────────────
FROM nginx:1.27-alpine
COPY --from=build /app/dist /usr/share/nginx/html
ARG NGINX_CONF=nginx.conf
COPY ${NGINX_CONF} /etc/nginx/conf.d/default.conf
EXPOSE 80
