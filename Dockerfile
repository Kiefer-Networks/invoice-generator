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

FROM debian:trixie-20260824-slim@sha256:d7e12182ce18b85b93007c1dedf31f2d29e01ccf3182cc4017c709b6259bc132 AS runtime
# Timestamped signed repositories also freeze the complete dependency closure.
RUN rm /etc/apt/sources.list.d/debian.sources && \
    printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/20260906T000000Z trixie main\ndeb [check-valid-until=no] http://snapshot.debian.org/archive/debian-security/20260906T000000Z trixie-security main\n' > /etc/apt/sources.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates chromium=152.0.7977.82-1~deb13u1 openjdk-21-jdk-headless=21.0.12.1+1-1~deb13u1 fonts-liberation curl && \
    rm -rf /var/lib/apt/lists/* && \
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
