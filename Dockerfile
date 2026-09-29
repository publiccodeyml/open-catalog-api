#
# This is for local development only.
# See Dockerfile.goreleaser for the image published on release or staging.
#

FROM golang:1.27@sha256:3680233e3204827fbdc66088528ae6d4b3d034f51d03a99d454f6de034888244 AS base

SHELL ["/bin/bash", "-o", "pipefail", "-euxc"]

WORKDIR /opt/app/api

CMD ["go", "run", "."]
