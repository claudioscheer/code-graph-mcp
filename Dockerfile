FROM golang:1.26-bookworm AS go-build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN go build -o /out/codegraph ./cmd/codegraph

FROM node:22.22-bookworm-slim
RUN corepack enable pnpm
WORKDIR /app
COPY package.json pnpm-lock.yaml* ./
RUN pnpm install --frozen-lockfile=false
COPY extractors ./extractors
COPY --from=go-build /out/codegraph /usr/local/bin/codegraph
ENTRYPOINT ["codegraph"]
