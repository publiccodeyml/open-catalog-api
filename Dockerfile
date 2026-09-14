#
# This is for local development only.
# See Dockerfile.goreleaser for the image published on release or staging.
#

FROM golang:1.27@sha256:f44f6e88636cfb311f9ebace870ded69d943f227bb3cb27d32ffd84ea18c43ea AS base

SHELL ["/bin/bash", "-o", "pipefail", "-euxc"]

WORKDIR /opt/app/api

CMD ["go", "run", "."]
