# syntax=docker/dockerfile:1.7
#
# loose-ends service image: the single Go binary (CLI + loopback/tailnet HTTP
# service) on a minimal Debian runtime.
#
# It joins your Tailscale network in USERSPACE via tailscale.com/tsnet, so the
# container needs NO /dev/net/tun, NO CAP_NET_ADMIN, and NO --privileged.
# (Verified against tsnet v1.78.3: it always runs the gVisor userspace netstack
# with a fake TUN + a no-op router; the only requirement is outbound TLS/443 to
# the Tailscale control plane and DERP relays.)
#
# Build + run via deploy/docker/docker-compose.yml — see deploy/docker/README.md.

ARG GO_VERSION=1.23.4

# ---------------------------------------------------------------------------
# Stage: builder — compile a static, CGO-free loose-ends binary.
# All deps (pgx, tsnet, chi, golang-migrate, toml) are pure Go, so CGO is off
# and the result runs on any minimal base with just CA certs.
# ---------------------------------------------------------------------------
FROM golang:${GO_VERSION}-bookworm AS builder

ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=local

WORKDIR /src

# Resolve modules in their own layer so source edits don't re-download deps.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# -trimpath: reproducible paths.  -ldflags "-s -w": drop symbol table + DWARF.
# -tags ts_omit_aws,ts_omit_kube: drop the AWS-SSM and Kubernetes tsnet state
# stores (and the entire aws-sdk-go-v2 tree). loose_ends persists tsnet state to
# a plain file path, which never matches the arn:/kube: store prefixes, so these
# are behavior-neutral here and meaningfully shrink the binary. If a build ever
# objects to them, removing the -tags is a safe no-op change.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags "-s -w" -tags "ts_omit_aws,ts_omit_kube" \
      -o /out/loose-ends ./cmd/loose-ends

# ---------------------------------------------------------------------------
# Stage: runtime — minimal Debian with CA certs + a non-root service user.
# debian-slim (not distroless) keeps a shell + apt for `docker compose exec`
# debugging; the binary is static so the base only needs ca-certificates.
# ---------------------------------------------------------------------------
FROM debian:bookworm-slim

# tsnet dials the Tailscale control plane + DERP over TLS — it needs CA roots.
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/*

# Non-root service account with a writable HOME. The tsnet state dir is
# pre-created and owned by this user so the named volume Docker initializes there
# inherits the ownership and tsnet (uid 10001) can persist tailscaled.state.
RUN useradd --uid 10001 --create-home --home-dir /home/loose-ends \
            --shell /usr/sbin/nologin loose-ends \
  && mkdir -p /home/loose-ends/.config/loose-ends/tsnet-state \
  && chown -R loose-ends:loose-ends /home/loose-ends \
  && chmod 700 /home/loose-ends/.config/loose-ends/tsnet-state

COPY --from=builder /out/loose-ends /usr/local/bin/loose-ends

USER loose-ends
ENV HOME=/home/loose-ends
WORKDIR /home/loose-ends

# Documentational only: the service is reached over the tailnet, so no host port
# is published. 17890 is the default loopback + tailnet listener port.
EXPOSE 17890

# `serve` auto-applies the embedded migrations, then serves on loopback and (per
# config) the tailnet. `docker stop` sends SIGTERM → graceful shutdown.
ENTRYPOINT ["loose-ends"]
CMD ["serve"]
