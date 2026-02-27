# Stage 1: Build
FROM node:22-alpine AS build
WORKDIR /app
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Serve (non-root Chainguard nginx, listens on 8080)
FROM cgr.dev/chainguard/nginx:latest
COPY build/web/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/build /usr/share/nginx/html
EXPOSE 8080
