# ---- build stage ----
FROM golang:1.25 AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /server ./cmd/server
RUN CGO_ENABLED=0 go build -o /ingest ./cmd/ingest

# ---- server run stage ----
FROM gcr.io/distroless/static-debian12 AS server
WORKDIR /app
COPY --from=build /server /server
COPY --from=build /app/web ./web
EXPOSE 8080
ENTRYPOINT ["/server"]

# ---- ingest run stage ----
FROM gcr.io/distroless/static-debian12 AS ingest
COPY --from=build /ingest /ingest
ENTRYPOINT ["/ingest"]
