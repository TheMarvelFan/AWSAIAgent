# syntax=docker/dockerfile:1.7
#
# Frontend image: the Vite build, served by nginx behind the same ALB as the
# API. Because .env.production points at relative paths (/v1), the bundle
# carries no hostname in it — the same image works behind any origin.

FROM node:22-alpine AS build
WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY . .

# `npm run build` is `tsc -b && vite build`. Vite loads .env.production on its
# own for a production build; do not add build args for VITE_* unless you want
# a second source of truth for the API base.
RUN npm run build

FROM nginx:1.27-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
