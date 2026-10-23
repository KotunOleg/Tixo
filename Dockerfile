FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/tixo ./cmd/tixo
RUN mkdir -p /out/data/uploads

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/tixo /tixo
COPY --from=build --chown=nonroot:nonroot /out/data /data
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/tixo"]
CMD ["serve"]
