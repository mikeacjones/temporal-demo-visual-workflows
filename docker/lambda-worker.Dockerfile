# The flow worker for AWS Lambda. Temporal Cloud invokes it when a flow has
# work; the platform wraps this image with its secrets loader.
FROM --platform=linux/amd64 golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -tags lambda.norpc -ldflags="-s -w" -o /out/lambda-worker ./cmd/lambda-worker

FROM --platform=linux/amd64 alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /out/lambda-worker /lambda-worker
ENTRYPOINT ["/lambda-worker"]
