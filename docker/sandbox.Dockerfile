# Simulated third-party APIs that the example flows call.
FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/sandbox ./cmd/sandbox
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sandbox ./cmd/sandbox

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/sandbox /sandbox
EXPOSE 8090
ENTRYPOINT ["/sandbox"]
