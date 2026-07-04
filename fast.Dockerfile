# Fast-path image: package a binary that was already compiled on the host, reusing the host's
# warm Go build cache. The build helper (internal/build) compiles ./cmd/<bin> from a sibling
# source tree and drops the result into this Dockerfile's build context, so a rebuild after a
# local source edit is a `go build` + a single COPY layer — no in-container module download, no
# full image rebuild.
#
# Used for both the oteldb server and the operator manager (BIN selects which). The binary is
# installed at /<BIN>:
#   - oteldb: the CR sets no command, so the container runs this image's ENTRYPOINT; the shell
#     wrapper execs /oteldb and forwards the operator-appended args (e.g. --config=...).
#   - operator: the manager Deployment overrides command with ["/manager"], bypassing the
#     ENTRYPOINT, so /manager simply has to exist at the root.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates

ARG BIN=oteldb
COPY ${BIN} /${BIN}

# Create an /etc/passwd entry for the non-root UID. oteldb (go-faster/sdk resource detection)
# calls user.Current(), which — in a CGO-disabled binary — needs a passwd entry to resolve the
# running UID; the distroless :nonroot base the release images use ships one, so mirror that here.
RUN adduser -D -u 65532 nonroot
USER 65532:65532

ENV BIN=${BIN}
# Exec form with a shell wrapper: appended container args ($@) are forwarded to the binary.
ENTRYPOINT ["/bin/sh", "-c", "exec /$BIN \"$@\"", "--"]
