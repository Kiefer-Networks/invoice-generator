# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32
FROM golang:1.27.1-trixie@sha256:9baa6b4187bbb98d240372a8a235ac0bb6b5ddd52bba1431dc2f7c0705862728 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY internal ./internal
RUN CGO_ENABLED=0 go test -tags=visual -c -trimpath -o /visual.test ./internal/render

# Keep these pins aligned with the production Dockerfile. The snapshot freezes
# fonts, Poppler and their transitive dependencies as well as Chromium.
FROM debian:trixie-20260824-slim@sha256:d7e12182ce18b85b93007c1dedf31f2d29e01ccf3182cc4017c709b6259bc132
RUN rm /etc/apt/sources.list.d/debian.sources && \
    printf 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/20260906T000000Z trixie main\ndeb [check-valid-until=no] http://snapshot.debian.org/archive/debian-security/20260906T000000Z trixie-security main\n' > /etc/apt/sources.list && \
    apt-get update && apt-get install -y --no-install-recommends chromium=152.0.7977.82-1~deb13u1 fonts-liberation fontconfig poppler-utils && \
    rm -rf /var/lib/apt/lists/*
COPY docker/visual-fonts.conf /etc/fonts/local.conf
RUN fc-cache -f && test "$(fc-match system-ui -f '%{family}')" = 'Liberation Sans'
COPY --from=build --chmod=0555 /visual.test /usr/local/bin/visual.test
COPY internal/render/testdata/visual /golden
ENV HOME=/tmp INVOICE_CHROME=/usr/bin/chromium INVOICE_VISUAL_RUNTIME=debian-20260906-chromium-152 TZ=UTC LANG=C.UTF-8
USER 65532:65532
WORKDIR /tmp
ENTRYPOINT ["/usr/local/bin/visual.test", "-test.run=^TestVisual", "-test.v", "-test.timeout=3m"]
