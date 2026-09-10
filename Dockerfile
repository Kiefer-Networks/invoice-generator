# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32
FROM --platform=$BUILDPLATFORM golang:1.27.1-trixie@sha256:9baa6b4187bbb98d240372a8a235ac0bb6b5ddd52bba1431dc2f7c0705862728 AS build
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum LICENSE ./
RUN go mod download && go mod verify
COPY cmd ./cmd
COPY internal ./internal
COPY testdata/dev ./testdata/dev
COPY --chmod=0555 docker/licenses /usr/local/bin/collect-licenses
RUN /usr/local/bin/collect-licenses
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags=production -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/server ./cmd/server

FROM build AS development-build
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/development ./cmd/server && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go test -c -trimpath -o /out/browser.test ./internal/web

FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS runtime
RUN apk add --no-cache ca-certificates=20260611-r0 chromium=152.0.7977.82-r0 openjdk21-jdk=21.0.12_p8-r0 font-liberation=2.1.5-r2 curl=8.22.0-r0 libcrypto3=3.5.8-r0 libssl3=3.5.8-r0 && \
    mkdir -p /data/database /data/documents /backup /config /development && \
    chown -R 65532:65532 /data /backup /development && chmod 0700 /data /data/database /data/documents /backup /development
ENV HOME=/tmp INVOICE_CHROME=/usr/bin/chromium
COPY --chmod=0555 docker/entrypoint docker/healthcheck /usr/local/bin/
COPY LICENSE /usr/share/doc/invoice-generator/LICENSE
COPY internal/zugferd/schema/LICENSE.txt /usr/share/doc/invoice-generator/CII-LICENSE.txt
USER 65532:65532
WORKDIR /data
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=15s --timeout=5s --start-period=90s --retries=3 CMD ["/usr/local/bin/healthcheck"]
ENTRYPOINT ["/usr/local/bin/entrypoint"]
CMD ["serve"]

FROM runtime AS development
COPY --from=development-build --chmod=0555 /out/development /usr/local/bin/server
COPY --from=development-build --chmod=0555 /out/browser.test /usr/local/bin/browser.test
COPY internal/web/templates /development-assets/templates
COPY internal/web/static /development-assets/static
CMD ["serve", "-dev", "-dev-root", "/development/state", "-dev-assets", "/development-assets"]

FROM runtime AS production
COPY --from=build /out/licenses /usr/share/doc/invoice-generator/licenses
COPY --from=build --chmod=0555 /out/server /usr/local/bin/server
