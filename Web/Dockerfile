# syntax=docker/dockerfile:1.7

FROM node:20-alpine AS builder
WORKDIR /app
ENV NODE_ENV=development

COPY package.json package-lock.json ./
RUN npm config set fund false \
 && npm config set audit false \
 && npm ci --include=dev

COPY . .
RUN npm run build

FROM nginx:1.27-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 80
