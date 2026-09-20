# syntax=docker/dockerfile:1.7
#
# Backend image: the Go API, the in-process deployment worker, and the terraform
# binary it shells out to.
#
# The catalog templates, the SQL migrations and the terraform modules are all
# go:embed'd into the binary, so nothing but the binary and terraform itself
# needs to reach the runtime stage.

# ---- terraform --------------------------------------------------------------
# 1.10+ is a hard requirement: the S3 backend uses use_lockfile for native state
# locking, and older versions reject the flag.
FROM hashicorp/terraform:1.13 AS terraform

# ---- build ------------------------------------------------------------------
# Keep this in step with the `go` line in go.mod.
FROM golang:1.27-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# ---- runtime ----------------------------------------------------------------
FROM alpine:3.21

# ca-certificates for the AWS SDK and the S3 backend; wget comes from busybox
# and is what the container health check uses.
RUN apk add --no-cache ca-certificates tzdata

COPY --from=terraform /bin/terraform /usr/local/bin/terraform
COPY --from=build     /out/api       /usr/local/bin/api

# TF_WORK_ROOT holds the saved plan file between plan and apply, so it must be
# writable by the process user.
#
# TF_PLUGIN_CACHE_DIR must EXIST before terraform runs. Terraform does not
# create it, and it does not warn — it silently skips caching and re-downloads
# the ~800 MB AWS provider into every workspace.
RUN mkdir -p /var/tmp/ai-aws-architect /var/tmp/tf-plugin-cache \
 && addgroup -g 10001 app \
 && adduser -D -u 10001 -G app app \
 && chown -R app:app /var/tmp/ai-aws-architect /var/tmp/tf-plugin-cache

USER app

ENV TF_WORK_ROOT=/var/tmp/ai-aws-architect \
    TF_PLUGIN_CACHE_DIR=/var/tmp/tf-plugin-cache \
    TERRAFORM_BINARY=/usr/local/bin/terraform \
    HTTP_ADDR=:8080

EXPOSE 8080

# No AWS_PROFILE anywhere. The SDK default chain must land on the ECS task role.
ENTRYPOINT ["/usr/local/bin/api"]
