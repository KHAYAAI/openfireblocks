# Multi-stage build for the NestJS API gateway.
FROM node:22-alpine AS build
WORKDIR /app

# Install all deps (including dev) to compile TypeScript.
COPY package*.json ./
RUN npm ci

COPY tsconfig*.json nest-cli.json ./
COPY src ./src
RUN npm run build

# Prune to production dependencies for the runtime image.
RUN npm ci --omit=dev

FROM node:22-alpine AS runtime
WORKDIR /app
ENV NODE_ENV=production
COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist
COPY --from=build /app/package.json ./package.json

EXPOSE 3000
USER node
CMD ["node", "dist/main.js"]
