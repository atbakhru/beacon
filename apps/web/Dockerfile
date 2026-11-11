FROM node:22-bookworm-slim AS build
WORKDIR /app
COPY package*.json ./
RUN --mount=type=secret,id=proxy_ca,target=/usr/local/share/ca-certificates/session.crt sh -c 'if [ -f /usr/local/share/ca-certificates/session.crt ]; then export NODE_EXTRA_CA_CERTS=/usr/local/share/ca-certificates/session.crt; fi; npm ci'
COPY . .
RUN npm run build
FROM nginx:1.27-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
