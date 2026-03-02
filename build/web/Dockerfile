# Stage 1: Build
FROM node:22-alpine@sha256:e4bf2a82ad0a4037d28035ae71529873c069b13eb0455466ae0bc13363826e34 AS build
WORKDIR /app
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Serve (non-root Chainguard nginx, listens on 8080)
FROM cgr.dev/chainguard/nginx:latest@sha256:5c89cba9db483e4649908db2a2f7f2be7735926bcc6dec35592bca7734617d1c
COPY build/web/nginx.conf /etc/nginx/conf.d/nginx.default.conf
COPY --from=build /app/build /usr/share/nginx/html
EXPOSE 8080
